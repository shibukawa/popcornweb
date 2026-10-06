package pwfast

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// shortGrace shortens how long a silent connection is spared once a stop has
// begun, so a test that waits past it does not wait a second.
func shortGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	previous := quietGrace
	quietGrace = grace
	t.Cleanup(func() { quietGrace = previous })
}

// oneShot opens a connection per request and closes it after the answer. The
// default client keeps what it opens and sometimes dials a spare it never uses,
// which is the state these tests exist to make on purpose; a client that cannot
// leave a connection behind keeps the connections under test the ones the test
// opened itself.
var oneShot = &http.Client{
	Transport: &http.Transport{DisableKeepAlives: true},
	Timeout:   5 * time.Second,
}

func getBody(base, path string) (string, error) {
	response, err := oneShot.Get(base + path)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return string(body), err
}

// running is a Run that owns a port nothing else holds.
type running struct {
	addr   string
	cancel context.CancelFunc
	done   chan error
}

func (r *running) base() string { return "http://" + r.addr }

// stop cancels the context and waits for Run to return, failing if it has not
// within the time given. The limit is the assertion: it sits well below the
// shutdown timeout, so a stop that waited the whole deadline fails here.
func (r *running) stop(t *testing.T, within time.Duration) {
	t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(within):
		t.Fatalf("Run did not return within %v of its context being cancelled", within)
	}
}

func runOnAFreePort(t *testing.T, handler fasthttp.RequestHandler) *running {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	parsed(t)
	server := pwconfig.Value[pwconfig.ServerConfig]()
	server.Port = port
	_, restore := pwconfig.Swap(server)
	t.Cleanup(restore)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &running{addr: "127.0.0.1:" + itoa(port), cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- Run(ctx, handler) }()

	var last error
	for attempt := 0; attempt < 100; attempt++ {
		if _, last = getBody(r.base(), "/hello"); last == nil {
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the server never answered: %v", last)
	return nil
}

// A connection that was accepted and never sent a byte is not something a
// graceful stop should wait for.
//
// fasthttp counts a connection as idle once a request has finished on it, and
// closes those as soon as it is told to stop. One that has not begun a request
// has no such moment, so it is waited for as though a request were on its way,
// for the whole shutdown timeout and then as a failure. Browsers hold spare
// connections open for exactly this reason, and so do the probes of some load
// balancers, so a deploy's SIGTERM would wait on every one of them.
//
// This is what TestRunServesAndShutsDownOnCancellation was failing on, one run
// in four: its client dials a spare connection while a pooled one is being
// returned, and parks it unused. That test arrives at the state by timing; this
// one makes it.
func TestRunStopsPromptlyWithAConnectionThatNeverSpoke(t *testing.T) {
	shortGrace(t, 100*time.Millisecond)
	r := runOnAFreePort(t, hello())

	silent, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	// Connections are accepted in the order they arrive, so an answered request
	// from a later one proves this one is already in the server's hands. Without
	// that the stop could reach it still queued, which the kernel resets rather
	// than waits on, and the test would pass without having tested anything.
	if body, err := getBody(r.base(), "/hello"); err != nil || body != "hello" {
		t.Fatalf("a later request answered %q, %v", body, err)
	}

	r.stop(t, 3*time.Second)

	// The server ended it, rather than leaving the process exit to.
	_ = silent.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := silent.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("the silent connection was left open: read returned %v", err)
	}
}

// A connection that a request has finished on, and the client keeps open, is
// not waited for either. The server closes those itself the moment it is told to
// stop, so this holds the grace for silent connections far above the limit,
// which leaves only that handling to pass it. It guards the day a newer fasthttp
// changes it.
func TestRunStopsPromptlyWithAnIdleKeepAliveConnection(t *testing.T) {
	shortGrace(t, time.Minute)
	r := runOnAFreePort(t, hello())

	conn, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /hello HTTP/1.1\r\nHost: t\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.Close || string(body) != "hello" {
		t.Fatalf("the connection was not kept alive for the next request: close=%v body=%q", response.Close, body)
	}

	r.stop(t, 2*time.Second)
}

// The other half of the contract, which ending silent connections must not be
// bought at the price of: a request that has begun is finished, however long the
// stop has been waiting.
func TestRunStopFinishesARequestInFlight(t *testing.T) {
	shortGrace(t, 100*time.Millisecond)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	mux := NewServeMux()
	mux.HandleFunc("GET /hello", func(r *fasthttp.RequestCtx) { _, _ = r.WriteString("hello") })
	mux.HandleFunc("GET /slow", func(r *fasthttp.RequestCtx) {
		once.Do(func() { close(entered) })
		<-release
		_, _ = r.WriteString("finished")
	})
	r := runOnAFreePort(t, mux.Handler)

	answered := make(chan string, 1)
	go func() {
		body, err := getBody(r.base(), "/slow")
		if err != nil {
			body = "error: " + err.Error()
		}
		answered <- body
	}()
	<-entered
	r.cancel()

	// Several times the grace, so a stop that took this connection for a silent
	// one has acted on it by now.
	select {
	case err := <-r.done:
		t.Fatalf("Run returned (%v) while a request was still being answered", err)
	case <-time.After(5 * quietGrace):
	}
	close(release)

	if body := <-answered; body != "finished" {
		t.Errorf("the request in flight was answered %q, want it finished", body)
	}
	r.stop(t, 3*time.Second)
}

// A request that is still arriving has begun too, though its handler has not
// run, which is the case that separates telling the two apart by bytes from
// telling them apart by handler.
func TestRunStopFinishesARequestStillArriving(t *testing.T) {
	shortGrace(t, 200*time.Millisecond)
	r := runOnAFreePort(t, hello())

	conn, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /hello HTTP/1.1\r\nHost: t\r\nConnection: close\r\n")); err != nil {
		t.Fatal(err)
	}
	// Past the grace, with the request head still unfinished.
	time.Sleep(20 * time.Millisecond)
	r.cancel()
	select {
	case err := <-r.done:
		t.Fatalf("Run returned (%v) while a request was still arriving", err)
	case <-time.After(5 * quietGrace):
	}

	if _, err := conn.Write([]byte("\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	answer, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if got := string(answer); len(got) < 12 || got[:12] != "HTTP/1.1 200" {
		t.Errorf("the request that was arriving was answered %q, want a 200", got)
	}
	r.stop(t, 3*time.Second)
}

// logRecord keeps what a server reports through its logger.
type logRecord struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRecord) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logRecord) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// Ending a connection that never spoke is the stop doing its work, and the
// server must not report it as a failure to serve. Closed plainly, it is logged
// once per connection as a read on a closed network connection, so a deploy with
// a few browsers attached would print a few errors for nothing having gone wrong.
func TestEndingASilentConnectionIsNotReportedAsAnError(t *testing.T) {
	shortGrace(t, 20*time.Millisecond)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reported := &logRecord{}
	closed := make(chan struct{}, 1)
	server := &fasthttp.Server{
		Handler: func(*fasthttp.RequestCtx) {},
		Logger:  reported,
		// The server announces a connection closed after it has finished reporting
		// on it, so waiting for this is waiting for whatever it was going to log.
		ConnState: func(_ net.Conn, state fasthttp.ConnState) {
			if state == fasthttp.StateClosed {
				closed <- struct{}{}
			}
		},
	}
	drain := newDrainListener(listener)
	go func() { _ = server.Serve(drain) }()

	silent, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	// Accepted, which is when it is tracked; a stop that met it still queued would
	// have nothing to end and the test nothing to see.
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(time.Millisecond) {
		drain.mu.Lock()
		tracked := len(drain.conns)
		drain.mu.Unlock()
		if tracked == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the connection was never accepted")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := drain.shutdown(ctx, server); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the server never finished with the connection")
	}
	if lines := reported.all(); len(lines) != 0 {
		t.Errorf("ending a connection that never spoke was reported: %q", lines)
	}
}

// Serve is the entry for a caller that owns its own listener, and a stop there
// has no deadline to fall back on at all: the wait on a connection that never
// spoke would not end.
func TestServeStopsWithAConnectionThatNeverSpoke(t *testing.T) {
	shortGrace(t, 100*time.Millisecond)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, listener, func(r *fasthttp.RequestCtx) { _, _ = r.WriteString("hello") })
	}()
	base := "http://" + listener.Addr().String()

	silent, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	if body, err := getBody(base, "/"); err != nil || body != "hello" {
		t.Fatalf("a later request answered %q, %v", body, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want a clean shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
}
