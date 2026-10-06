package pwfast

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwdatabase"
	"github.com/shibukawa/popcornweb/pwextension"
	"github.com/shibukawa/popcornweb/pwobservability"
	"github.com/shibukawa/popcornweb/pwratelimit"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/pwsession"
	"github.com/shibukawa/tinybind-go/fasthttpbind"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// Option configures startup.
type Option func(*startOptions) error

type startOptions struct {
	publicFS fs.FS
	contribs []func(RuntimeOptions) RuntimeOptions
	setups   []func(context.Context) (func(RuntimeOptions) RuntimeOptions, error)
}

// WithPublicFS supplies the embedded public tree, rooted at its public
// directory. An embed is a fact of the binary rather than of a settings file,
// which is why it is passed rather than configured.
func WithPublicFS(publicFS fs.FS) Option {
	return func(options *startOptions) error {
		if publicFS == nil {
			return errors.New("popcornweb: nil public filesystem")
		}
		options.publicFS = publicFS
		return nil
	}
}

// WithRuntimeOptions folds one contribution into what the chain is built from.
//
// It is how a plugin that installs frames reaches this transport. There is no
// extension registry here — an imported capability cannot install a frame this
// chain would gain without the application saying so — which is why a plugin
// serving both transports registers on the other one and is named here. An
// application's own middleware has RegisterMiddleware instead. An
// authentication plugin's Apply is exactly this shape:
//
//	auth, err := authfast.Setup(ctx)
//	pwfast.Run(ctx, handler, pwfast.WithRuntimeOptions(auth.Apply))
func WithRuntimeOptions(apply func(RuntimeOptions) RuntimeOptions) Option {
	return func(options *startOptions) error {
		if apply == nil {
			return errors.New("popcornweb: nil runtime option contribution")
		}
		options.contribs = append(options.contribs, apply)
		return nil
	}
}

// WithSetup folds in a contribution that has to be built from the resolved
// configuration first.
//
// It is the shape a plugin with startup of its own needs. WithRuntimeOptions
// takes a contribution the caller already has; this takes the thing that makes
// one, and is handed a context carrying the settings, the logger and the pool —
// the same capsule a request is served with. An authentication plugin's
// Contribute is exactly this shape:
//
//	pwfast.Run(ctx, handler, pwfast.WithSetup(authfast.Contribute))
//
// It runs after the shared layers and before the chain is composed, so a plugin
// reads a pool that is open and a session manager that exists.
func WithSetup(setup func(context.Context) (func(RuntimeOptions) RuntimeOptions, error)) Option {
	return func(options *startOptions) error {
		if setup == nil {
			return errors.New("popcornweb: nil runtime setup")
		}
		options.setups = append(options.setups, setup)
		return nil
	}
}

// Start performs framework initialization and returns the request chain
// together with the shutdown that releases what it opened.
//
// It is the second transport's counterpart to pw.Middlewares, and it is what an
// application calls when it owns its own listener. Run calls it and owns one.
//
// Every layer it starts is a shared one — pwconfig, pwdatabase, pwsession,
// pwobservability, pwratelimit — so this reproduces none of that startup and
// only sequences it. What is genuinely this transport's is the last step, which
// is the chain.
//
// The shutdown is returned rather than deferred because the caller decides when
// serving stops. It runs the cleanups in reverse, and is safe to call once.
func Start(ctx context.Context, handler fasthttp.RequestHandler, option ...Option) (fasthttp.RequestHandler, func(context.Context) error, error) {
	if ctx == nil {
		return nil, nil, errors.New("popcornweb: nil context")
	}
	if handler == nil {
		return nil, nil, errors.New("popcornweb: nil handler")
	}
	options := startOptions{}
	for _, apply := range option {
		if apply == nil {
			continue
		}
		if err := apply(&options); err != nil {
			return nil, nil, err
		}
	}

	started := &startup{}
	chain, err := start(ctx, handler, options, started)
	if err != nil {
		// Whatever opened before the failure is closed here rather than left to
		// the caller, which holds no handle on any of it.
		return nil, nil, errors.Join(err, started.close(context.WithoutCancel(ctx)))
	}
	return chain, started.close, nil
}

// startup collects what has to be released, in the order it was opened.
type startup struct {
	cleanups []namedCleanup
	closed   bool
}

type namedCleanup struct {
	name  string
	close func(context.Context) error
}

func (s *startup) add(name string, close func(context.Context) error) {
	if close != nil {
		s.cleanups = append(s.cleanups, namedCleanup{name: name, close: close})
	}
}

// close releases in reverse, and keeps going past a failure: a store that
// cannot close must not strand the pool opened before it.
func (s *startup) close(ctx context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	var result error
	for index := len(s.cleanups) - 1; index >= 0; index-- {
		if err := s.cleanups[index].close(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("close %s: %w", s.cleanups[index].name, err))
		}
	}
	return result
}

func start(ctx context.Context, handler fasthttp.RequestHandler, options startOptions, started *startup) (fasthttp.RequestHandler, error) {
	if err := pwconfig.Parse(); err != nil {
		return nil, err
	}
	// An action the caller cannot answer is refused rather than left pending.
	// A caller that owns its own listener gets a chain back from here, and a
	// health probe falling through into one would bind a second time on every
	// HEALTHCHECK interval and report that as the server's own health.
	if err := pwconfig.RefusePendingFrameworkAction(); err != nil {
		return nil, err
	}

	// The CBOR body cap is process-wide in the binding runtime and shared with
	// the other transport, so it is applied once at startup rather than woven
	// into the chain. Zero restores the runtime's own 1 MiB default, which
	// makes the unconditional call also the unconfigured case.
	fasthttpbind.SetMaxCBORBodyBytes(pwconfig.Value[pwconfig.ServerConfig]().CBORMaxBody)

	// The pool first, because the session backend and anything a plugin opens
	// may read it, and because a deployment that cannot reach its database
	// should fail before a port is bound.
	if rdb := pwconfig.Value[pwconfig.MiddlewareConfig]().RDB; rdb.Enabled && pwdatabase.Connections() == nil {
		if err := pwdatabase.Start(rdb); err != nil {
			return nil, err
		}
		started.add("database", func(context.Context) error { return pwdatabase.Close() })
	}

	observability, err := pwobservability.Build(pwconfig.Value[pwconfig.ObservabilityConfig](), pwconfig.Env())
	if err != nil {
		return nil, err
	}
	for _, cleanup := range observability.Cleanups() {
		started.add(cleanup.Name, cleanup.Close)
	}

	resources := runtimeResources(observability)
	// Everything after this reads settings, the logger and the pool through the
	// capsule, exactly as a request does.
	setupCtx := pwruntime.WithResources(ctx, resources)

	// A root span is created when export exists, and also when configuration
	// asked for framework spans outright: the children below are only a trace if
	// something roots them.
	observabilityConfig := pwconfig.Value[pwconfig.ObservabilityConfig]()
	runtime := RuntimeOptions{
		Resources: resources,
		Tracing:   observability.Tracing() || pwobservability.TraceForced(observabilityConfig),
		PublicFS:  options.publicFS,
	}

	manager, err := pwsession.Setup(setupCtx)
	if err != nil {
		return nil, err
	}
	if manager != nil {
		started.add("session", pwsession.Close)
		runtime.Session = manager
		sessionConfig := pwconfig.Value[pwconfig.SessionConfig]()
		policy, err := pwsession.CookiePolicy(sessionConfig)
		if err != nil {
			return nil, err
		}
		// The CSRF companion is issued beside the session cookie and must travel
		// with the same policy, or one of the two is dropped by a browser the
		// other reaches.
		runtime.SessionCookie, runtime.SessionSameSite = policy, policy.SameSite
	}

	// The counter is opened here rather than inside the frame for the same
	// reason the pool is: a Redis backend dials a server and refuses to start
	// against one it cannot reach, which is startup rather than request work.
	if limits := pwconfig.Value[pwconfig.RateLimitConfig](); limits.Enabled {
		counter, closeCounter, err := pwratelimit.OpenStore(setupCtx, limits)
		if err != nil {
			return nil, err
		}
		started.add("ratelimit", closeCounter)
		runtime.RateLimitCounter = counter
	}

	// Imported capabilities that install no frame — a storage integration opens
	// its client here. One that does install a frame is refused by name rather
	// than dropped; see pwextension.SetupProcess.
	closeExtensions, err := pwextension.SetupProcess(setupCtx)
	started.add("extensions", closeExtensions)
	if err != nil {
		return nil, err
	}

	for _, setup := range options.setups {
		apply, err := setup(setupCtx)
		if err != nil {
			return nil, err
		}
		if apply != nil {
			runtime = apply(runtime)
		}
	}
	for _, apply := range options.contribs {
		runtime = apply(runtime)
	}
	return Middlewares(handler, runtime)
}

// runtimeResources is the capsule every request is served with.
func runtimeResources(observability *pwobservability.Resolved) pwruntime.Resources {
	config := pwconfig.Value[pwconfig.ObservabilityConfig]()
	db, driver := pwdatabase.Default()
	return pwruntime.Resources{
		Configs:     pwconfig.Snapshot(),
		Log:         observability.Backend(),
		DB:          db,
		DBDriver:    driver,
		Connections: pwdatabase.Connections(),
		Query:       pwobservability.QueryDiagnostics(config, pwconfig.Development()),
		Trace:       pwobservability.TracingPolicy(config, observability.Tracing()),
		// One configuration enforces one policy on either transport, so the
		// instrument set is resolved here exactly as pw resolves it. What differs
		// is only what this build renders: it commits after a buffered render, so
		// its render metrics carry the buffered mode and no boundary series.
		Metrics: pwobservability.MetricsPolicy(config, observability.MetricProvider(), observability.Tracing()),
	}
}

// Run owns parsing, framework initialization, serving, graceful shutdown and
// resource cleanup.
//
// It is what a generated entry point calls, and the second transport's
// counterpart to pw.Run. The listener is bound here rather than inside Serve so
// that the startup line reports the port that was actually accepted — which is
// the one an operator opens when the configuration asked for port 0.
func Run(ctx context.Context, handler fasthttp.RequestHandler, option ...Option) error {
	// The command line first. --generate-config, the health probe and whatever
	// subcommands the application registered are answered here rather than by
	// starting a server, and they are the same words the other build answers
	// because the parsing is the shared layer's.
	if err := pwconfig.Parse(); err != nil {
		return err
	}
	if handled, err := pwconfig.RunFrameworkAction(); handled {
		return err
	}
	chain, shutdown, err := Start(ctx, handler, option...)
	if err != nil {
		return err
	}
	config := pwconfig.Value[pwconfig.ServerConfig]()
	port, err := pwconfig.HostingPort(config.Port, os.LookupEnv)
	if err != nil {
		return errors.Join(err, shutdown(closingContext(config.ShutdownTimeout)))
	}
	config.Port = port

	signalContext, stopSignals := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	listener, err := net.Listen("tcp", ":"+strconv.Itoa(config.Port))
	if err != nil {
		return errors.Join(err, shutdown(closingContext(config.ShutdownTimeout)))
	}
	server := newServer(config, chain)
	pwobservability.ProcessLogger().Log(ctx, pwruntime.LevelInfo, "listening",
		pwruntime.String("address", listenURL(listener)),
		pwruntime.String("transport", "fasthttp"),
		pwruntime.String("environment", pwconfig.Env()))

	serveErr := serveUntil(signalContext, server, listener, config.ShutdownTimeout)
	return errors.Join(serveErr, shutdown(closingContext(config.ShutdownTimeout)))
}

// closingContext bounds shutdown independently of the context that ended
// serving, which is normally already cancelled by the signal that got here.
func closingContext(timeout time.Duration) context.Context {
	if timeout <= 0 {
		return context.Background()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	// The cancel is deliberately dropped after the timeout elapses rather than
	// deferred: the caller runs to completion inside it, and returning a live
	// context is the whole point.
	time.AfterFunc(timeout, cancel)
	return ctx
}

func newServer(config pwconfig.ServerConfig, handler fasthttp.RequestHandler) *fasthttp.Server {
	return &fasthttp.Server{
		Handler: handler,
		// The other transport announces nothing, so neither does this one. A
		// Server header names the transport to anybody who asks, and two builds
		// of one application should not be told apart by a response they both
		// intend to be the same.
		NoDefaultServerHeader: true,
		ReadTimeout:           config.ReadTimeout,
		WriteTimeout:          config.WriteTimeout,
		IdleTimeout:           config.IdleTimeout,
		MaxRequestBodySize:    int(config.MaxRequestBody),
	}
}

// serveUntil serves until the listener fails or the context ends, then drains.
//
// Draining waits for the requests being answered, up to the timeout, and does
// not wait for a connection that has sent nothing: see drainListener.
func serveUntil(ctx context.Context, server *fasthttp.Server, listener net.Listener, timeout time.Duration) error {
	drain := newDrainListener(listener)
	result := make(chan error, 1)
	go func() { result <- server.Serve(drain) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownCtx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			shutdownCtx, cancel = context.WithTimeout(shutdownCtx, timeout)
			defer cancel()
		}
		if err := drain.shutdown(shutdownCtx, server); err != nil {
			return err
		}
		return <-result
	}
}

// listenURL renders the address an operator can open. A wildcard or loopback
// bind is reported as localhost, because that is the host that resolves.
func listenURL(listener net.Listener) string {
	address := listener.Addr()
	tcp, ok := address.(*net.TCPAddr)
	if !ok {
		return "http://" + address.String()
	}
	host := tcp.IP.String()
	switch {
	case tcp.IP == nil || tcp.IP.IsUnspecified() || tcp.IP.IsLoopback():
		host = "localhost"
	case tcp.IP.To4() == nil:
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + strconv.Itoa(tcp.Port)
}
