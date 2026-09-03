package pw

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pwconfig"
	tinybind "github.com/shibukawa/tinybind-go"
	"github.com/shibukawa/tinygodriver/httpserver"
)

// Option configures framework lifecycle construction.
type Option func(*lifecycleOptions) error

type lifecycleOptions struct {
	publicFS fs.FS
}

// WithExternalAssets reads the external public tree from a store instead of
// the directory beside the process, per requirement:cloudflare-r2-storage.
// The generated Cloudflare Workers entry passes the bucket binding here; a
// process host, which has the directory, passes nothing.
func WithExternalAssets(source middlewares.ExternalAssetSource) Option {
	return func(*lifecycleOptions) error {
		if source == nil {
			return errors.New("popcornweb: nil external asset source")
		}
		middlewares.RegisterExternalAssetSource(source)
		return nil
	}
}

// WithPublicFS supplies the embedded public tree, rooted at its public directory.
func WithPublicFS(publicFS fs.FS) Option {
	return func(options *lifecycleOptions) error {
		if publicFS == nil {
			return errors.New("popcornweb: nil public filesystem")
		}
		options.publicFS = publicFS
		return nil
	}
}

// Middlewares performs framework initialization and returns the same wrapped
// handler stack used by Run. The startup summary is emitted here because the
// application owns the listener and the framework never learns its address.
func Middlewares(handler http.Handler, option ...Option) (http.Handler, error) {
	if err := ParseConfig(); err != nil {
		return nil, err
	}
	if err := refusePendingFrameworkAction(); err != nil {
		return nil, err
	}
	wrapped, err := buildMiddlewares(handler, option...)
	if err != nil {
		return nil, err
	}
	emitBootReport("")
	return wrapped, nil
}

func buildMiddlewares(handler http.Handler, option ...Option) (http.Handler, error) {
	if handler == nil {
		return nil, errors.New("popcornweb: nil handler")
	}
	if err := ParseConfig(); err != nil {
		return nil, err
	}
	options := lifecycleOptions{}
	for _, apply := range option {
		if apply == nil {
			continue
		}
		if err := apply(&options); err != nil {
			return nil, err
		}
	}
	// Updates key their validators with a secret, and an unkeyed digest of
	// low-entropy content lets a guess be confirmed by comparing digests. That
	// is refused before the port is bound rather than degraded at request time.
	if err := validateUpdateConfig(ConfigContext[HTMLConfig](nil)); err != nil {
		return nil, err
	}
	// A redraw endpoint is published by a generated init, which has nowhere to
	// return a collision to. This is where one is answered.
	if err := validateReloadableRegistration(); err != nil {
		return nil, err
	}
	server := ConfigContext[ServerConfig](nil)
	security := ConfigContext[SecurityConfig](nil)
	middleware := ConfigContext[MiddlewareConfig](nil)
	if err := validateConfiguredRuntime(); err != nil {
		return nil, err
	}
	// The CBOR body cap is process-wide in the binding runtime and shared by
	// both transports, so it is applied once here rather than woven into the
	// middleware chain. Zero restores the runtime's own 1 MiB default, which
	// makes the unconditional call also the unconfigured case.
	tinybind.SetMaxCBORBodyBytes(server.CBORMaxBody)
	if err := initializeRuntimeDatabase(); err != nil {
		return nil, err
	}
	if err := validateOperationalEndpointCollisions(handler, server); err != nil {
		return nil, err
	}
	observability := ConfigContext[ObservabilityConfig](nil)
	telemetry, err := buildObservability(observability, Env())
	if err != nil {
		return nil, err
	}
	// A root span is created when export exists, and also when configuration
	// asked for framework spans outright: the children below are only a trace if
	// something roots them.
	rootSpan := telemetry.Tracing() || traceForced(observability)
	resources := runtimeResources(telemetry.Backend(), telemetry.MetricProvider(), telemetry.Tracing())
	reportEnvironment()
	reportCompressionCodings(middleware)
	reportDatabaseConnections(resources.Connections)
	reportQueryDiagnostics(resources.Query, Env(), Development(), resources.DBDriver)
	// The data pane needs the pool, so it starts once the database is open and
	// before the first request. It is a no-op outside the pwdev build mode.
	startDevelopmentData(resources)
	wrapped, err := buildRuntimeHandler(handler, server, security, middleware, resources, rootSpan, options.publicFS)
	if err != nil {
		return nil, err
	}
	// The seed and assert endpoints wrap the finished chain rather than joining
	// it, so they exist only on the served application — the test bridge builds
	// its chain without them — and only in the pwdev build mode.
	return developmentTestEndpoints(wrapped, middleware, resources), nil
}

// Run owns parsing, framework initialization, serving, graceful shutdown, and
// resource cleanup.
func Run(ctx context.Context, handler http.Handler, option ...Option) error {
	if ctx == nil {
		return errors.New("popcornweb: nil context")
	}
	if err := ParseConfig(); err != nil {
		return err
	}
	if handled, err := runFrameworkAction(); handled {
		return err
	}
	serverConfig := ConfigContext[ServerConfig](nil)
	port, err := pwconfig.HostingPort(serverConfig.Port, os.LookupEnv)
	if err != nil {
		return err
	}
	serverConfig.Port = port
	wrapped, err := buildMiddlewares(handler, option...)
	if err != nil {
		return err
	}

	signalContext, cancelSignals := notifyShutdownSignals(ctx)
	defer cancelSignals()
	server := newHTTPServer(serverConfig, wrapped)
	// Binding here, instead of inside ListenAndServe, keeps the startup summary
	// honest: it is written once the port is actually accepted, and it reports
	// the resolved port even when the configuration asked for port 0 or a
	// development run moved off a port it could not bind.
	listener, err := listenApplication(serverConfig)
	if err != nil {
		return closeRuntimeResources(serverConfig.ShutdownTimeout, err)
	}
	address := listenURL(listener)
	emitBootReport(address)
	// The development console links the application, and the address it worked
	// out from the project files is not always the one this process bound.
	announceDevelopmentListener(address)
	// Not server.Serve. Under TinyGo, net/http starts a background read before
	// the handler and cancels it by moving a deadline into the past, which the
	// network driver cannot apply to a read already in flight — so Hijack blocks
	// forever and a WebSocket handshake hangs with no error and no log line.
	// This entry reads the request head itself and hands an upgrade a writer it
	// can hijack, while everything else still reaches a real http.Server. Under
	// host Go it is server.Serve and nothing more, so one line covers both.
	serve := func() error { return httpserver.Serve(listener, server) }
	serveErr := serveUntilContext(signalContext, server, listener, serve, serverConfig.ShutdownTimeout)
	return closeRuntimeResources(serverConfig.ShutdownTimeout, serveErr)
}

// hostingPort follows the port contract of HTTP-to-process serverless
// adapters. PORT is already handled by ServerConfig's configuration binding;
// the two variables here are stronger because their host sends traffic to the
// assigned port regardless of an application's config file.
//
// AWS Lambda Web Adapter uses AWS_LWA_PORT when present and otherwise falls
// back to PORT or 8080. Azure Functions custom handlers publish their assigned
// loopback port as FUNCTIONS_CUSTOMHANDLER_PORT. Keeping both translations at
// the listener boundary lets the same application binary remain an ordinary
// net/http server everywhere else.
func hostingPort(configured int, lookup func(string) (string, bool)) (int, error) {
	return pwconfig.HostingPort(configured, lookup)
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

func serveUntilContext(ctx context.Context, server *http.Server, listener net.Listener, serve func() error, shutdownTimeout time.Duration) error {
	result := make(chan error, 1)
	go func() { result <- serve() }()

	select {
	case err := <-result:
		return endedServing(err, false)
	case <-ctx.Done():
		// Closing the listener is what stops new connections arriving, and it
		// has to happen here rather than inside Shutdown: the upgrade-capable
		// serving path accepts on this listener itself and gives the server an
		// internal one, so Shutdown closes what the server owns and leaves the
		// accept loop running with nothing able to end it. Waiting on a serve
		// call that can no longer return is how a graceful stop becomes a hang.
		//
		// Under host Go the server closes the same listener a moment later,
		// which is harmless, so one order serves both paths.
		closeErr := listener.Close()
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		// Shutdown closes the listeners the server tracks, and the one above is
		// among them, so the second close reports a closed listener and Shutdown
		// hands that back as its own error. Whether it happens at all is a race
		// against Serve noticing the close and untracking first — which is why a
		// graceful stop reported this occasionally rather than every time, and
		// why the test covering it was flaky rather than failing.
		//
		// It is dropped for the reason the close above drops it: this process
		// closed that listener deliberately, one line earlier. A shutdown that
		// ran out of time still reports the deadline it missed, which is the
		// error worth keeping.
		shutdownErr := server.Shutdown(shutdownContext)
		if errors.Is(shutdownErr, net.ErrClosed) {
			shutdownErr = nil
		}
		cancel()
		serveErr := endedServing(<-result, true)
		return errors.Join(closeErr, shutdownErr, serveErr)
	}
}

// endedServing drops the errors that mean serving stopped because it was asked
// to, and keeps the ones that mean it stopped for some other reason.
//
// http.Server reports ErrServerClosed for both a Shutdown and a listener closed
// while shutting down. The upgrade-capable path runs its own accept loop and
// reports what Accept reported, which is the closed-listener error once the
// shutdown above has closed it — a stop this process asked for, so it is only
// dropped on that branch.
func endedServing(err error, requested bool) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if requested && errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func newHTTPServer(config ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + strconv.Itoa(config.Port),
		Handler:           handler,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
	}
}

func closeRuntimeResources(timeout time.Duration, err error) error {
	runtimeState.RLock()
	cleanups := append([]*runtimeCleanup(nil), runtimeState.cleanups...)
	runtimeState.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return errors.Join(err, runRuntimeCleanups(ctx, cleanups))
}

func runRuntimeCleanups(ctx context.Context, cleanups []*runtimeCleanup) error {
	var result error
	for index := len(cleanups) - 1; index >= 0; index-- {
		cleanupErr := cleanups[index].run(ctx)
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("close %s: %w", cleanups[index].name, cleanupErr)
		}
		result = errors.Join(result, cleanupErr)
	}
	return result
}
