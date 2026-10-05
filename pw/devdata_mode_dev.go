//go:build pwdev

package pw

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/shibukawa/popcornweb/pwdata"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// startDevelopmentData serves the data pane beside the application.
//
// It listens on a loopback address of its own rather than on the application's
// listener, so nothing here is reachable from the port the application serves,
// and it tells the console where it is rather than waiting to be found.
//
// A failure is reported and dropped. The pane is a reader of the application,
// and an application that runs without one is still an application.
func startDevelopmentData(resources pwruntime.Resources) {
	console := strings.TrimSpace(os.Getenv(DevConsoleURLVar))
	if console == "" {
		// No console is running, so there is nothing to announce to. This is
		// the ordinary case for a pwdev binary started by hand.
		return
	}
	// A pool served natively has no *sql.DB, so the connection set is asked as
	// well: the pane reads through each connection's executor, and refusing a
	// PostgreSQL project here told it that it had no database.
	if resources.DB == nil && (resources.Connections == nil || resources.Connections.Count() == 0) {
		// A console is running and expecting a pane, so silence here would
		// leave the developer looking at a pane that never attaches with
		// nothing to explain it.
		fmt.Fprintln(os.Stderr, "pw: development data pane: no database is configured, so there is nothing to serve")
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "pw: development data pane:", err)
		return
	}
	server := pwdata.New(developmentConnections(resources), Env())
	go func() {
		_ = (&http.Server{Handler: server.Handler()}).Serve(listener)
	}()
	announceDevelopmentData(console, listener.Addr().String())
}

// developmentConnections describes every pool the application opened.
//
// A configuration that declares a connection set gets one entry per connection
// rather than one per group: selection inside a group is round robin, so a pane
// addressing the group could not say which replica answered, and whether a
// replica has caught up is the one question replicas raise.
//
// The driver travels with each connection, because nothing forbids two groups
// on two engines and the dialect is resolved from it.
func developmentConnections(resources pwruntime.Resources) []pwdata.Connection {
	if resources.Connections == nil {
		return []pwdata.Connection{
			pwdata.NewConnection("default", "default", resources.DBDriver, false, resources.DB),
		}
	}
	var connections []pwdata.Connection
	for _, connection := range resources.Connections.Connections() {
		connections = append(connections, pwdata.NewConnection(
			connection.Label, connection.Group, connection.Driver, connection.ReadOnly, connection.Executor()))
	}
	if len(connections) == 0 {
		return []pwdata.Connection{
			pwdata.NewConnection("default", "default", resources.DBDriver, false, resources.DB),
		}
	}
	return connections
}

// announceDevelopmentData tells the console the address to proxy to.
func announceDevelopmentData(console, address string) {
	announceToDevConsole(console, "/api/attach", address, "development data pane")
}
