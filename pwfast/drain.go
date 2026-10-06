package pwfast

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibukawa/tinygodriver/fasthttp"
)

// quietGrace is how long a connection that has not sent a byte is left alone
// once a stop has begun. A variable so a test can shorten it.
//
// A client that has just connected is about to speak, and a stop must not turn
// its request away, so a young connection gets the time such a client could
// take: far longer than the gap between a handshake and the first byte of a
// request, and well inside the shutdown timeout. Past it, silence means the
// client is not going to — a browser holding a spare connection open, a probe
// that connected and left — and a stop has no reason to wait for it. net/http
// draws the same line for itself at five seconds; this one is shorter because an
// operator is waiting on the stop.
var quietGrace = time.Second

// sweepInterval is how often a stop looks for connections whose grace has run
// out. It bounds how much later than the grace one is ended.
const sweepInterval = 50 * time.Millisecond

// drainListener is the listener the server accepts from, wrapped so that a stop
// can tell a connection that has begun a request from one that has not.
//
// fasthttp ends a stop by closing the connections it counts as idle, which are
// the ones a request has finished on, and by waiting for the rest. A connection
// that has not carried a request is not among the idle: it is marked active as
// soon as serving it begins, before its first byte is read, so the stop waits
// for it — until the client speaks, until the read timeout, thirty seconds by
// default and longer than the shutdown timeout, or until the deadline. The
// deadline is the usual end, and it is reported as a failed shutdown.
//
// Only the bytes can say which connections those are. The server marks a
// connection active before it has read anything, so its states cannot, and a
// handler cannot either: a request still arriving has begun although no handler
// has run.
type drainListener struct {
	net.Listener

	mu    sync.Mutex
	conns map[*drainConn]struct{}
}

func newDrainListener(listener net.Listener) *drainListener {
	return &drainListener{Listener: listener, conns: make(map[*drainConn]struct{})}
}

func (l *drainListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	accepted := &drainConn{Conn: conn, listener: l, accepted: time.Now()}
	l.mu.Lock()
	l.conns[accepted] = struct{}{}
	l.mu.Unlock()
	return accepted, nil
}

func (l *drainListener) forget(conn *drainConn) {
	l.mu.Lock()
	delete(l.conns, conn)
	l.mu.Unlock()
}

// shutdown stops the server gracefully, and ends the connections that stop
// would otherwise wait on for the whole of its deadline.
//
// The server's own shutdown does the rest: it closes the listener, closes the
// connections a request has finished on, and waits for the requests still being
// answered. The sweep runs beside it rather than before it because the listener
// is still open until the server closes it, so a connection can arrive after
// any one pass.
func (l *drainListener) shutdown(ctx context.Context, server *fasthttp.Server) error {
	stopped := make(chan error, 1)
	go func() { stopped <- server.ShutdownWithContext(ctx) }()

	grace := quietGrace
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-stopped:
			return err
		case <-ticker.C:
			l.closeSilent(grace)
		}
	}
}

// closeSilent ends every connection that has been open for the grace and has
// not sent a byte.
func (l *drainListener) closeSilent(grace time.Duration) {
	cutoff := time.Now().Add(-grace)
	var silent []*drainConn
	l.mu.Lock()
	for conn := range l.conns {
		if !conn.spoke.Load() && !conn.accepted.After(cutoff) {
			silent = append(silent, conn)
		}
	}
	l.mu.Unlock()
	// Ended outside the lock, because closing forgets the connection under it.
	for _, conn := range silent {
		conn.end()
	}
}

// drainConn records whether anything has been read from the connection.
//
// It is a net.Conn and no more: the methods only a TCP connection has are not
// promoted, and nothing on this transport reaches for them. It serves no file
// from disk, which is where the sendfile path of a TCP connection would matter,
// and it asks the server for no keep-alive tuning.
type drainConn struct {
	net.Conn

	listener *drainListener
	accepted time.Time
	spoke    atomic.Bool
	ended    atomic.Bool
}

func (c *drainConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && !c.spoke.Load() {
		c.spoke.Store(true)
	}
	if err != nil && c.ended.Load() {
		// A connection the stop ended reads as a stream that ended, which is what
		// the server takes a client leaving for and passes over quietly. Left as
		// the closed-connection error it is logged as a failure to serve, once for
		// every connection a stop ends.
		return n, io.EOF
	}
	return n, err
}

// end closes the connection on behalf of a stop. It is marked first, so the read
// the close interrupts is already told why.
func (c *drainConn) end() {
	c.ended.Store(true)
	_ = c.Close()
}

func (c *drainConn) Close() error {
	c.listener.forget(c)
	return c.Conn.Close()
}
