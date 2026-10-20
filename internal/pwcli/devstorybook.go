package pwcli

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/shibukawa/popcornweb/pwstory"
)

// devStorybook is the harness process and the pane the console reaches it
// through.
//
// It is a process rather than something linked into pw because the templates it
// renders are generated Go in the project's own module, which only a binary
// compiled from that module can call.
//
// The pane exists before the process does. The console is built before the
// first generation run, and the harness is one of the things that run produces,
// so the handler is a stable indirection over an address filled in later. That
// also covers a restart: the pane keeps its place in the console while the
// process behind it is replaced.
type devStorybook struct {
	address atomic.Pointer[string]
	command *exec.Cmd
	exited  <-chan error
}

// handler proxies the pane to whatever the harness address currently is.
//
// The console owns the URL the developer opens, so the harness never appears as
// an address of its own. Before the first build finishes, and after a build that
// failed, the pane says so rather than the console appearing to have lost it.
func (s *devStorybook) handler() http.Handler {
	if s == nil {
		return nil
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			address := s.address.Load()
			if address == nil {
				// Rewrite has no way to fail, so an unset address is sent to a
				// host that cannot resolve and picked up by ErrorHandler.
				request.SetURL(&url.URL{Scheme: "http", Host: "storybook.invalid"})
				return
			}
			request.SetURL(&url.URL{Scheme: "http", Host: *address})
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w,
				"the storybook harness is not answering. It is generated and built with the project, "+
					"so it appears once a build has succeeded; a build that failed is reported on the console index.",
				http.StatusServiceUnavailable)
		},
	}
	return proxy
}

// start builds and runs the generated harness, replacing whatever was running.
//
// A project with no template tree generates no harness and gets none here. A
// harness that fails to build leaves the pane reporting its own absence and the
// loop untouched, because a storybook is a reader of the project rather than a
// part of it.
func (s *devStorybook) start(root string, styles []string, stdout, stderr io.Writer, configs ...assetsConfig) {
	var assets assetsConfig
	if len(configs) > 0 {
		assets = configs[0]
	}
	if s == nil {
		return
	}
	s.stop()
	if _, err := os.Stat(filepath.Join(root, storybookDirectory)); err != nil {
		return
	}
	port, err := reserveLoopbackPort()
	if err != nil {
		fmt.Fprintln(stderr, "pw dev: storybook:", err)
		return
	}
	address := "127.0.0.1:" + strconv.Itoa(port)
	command := exec.Command("go", "run", "-tags=pwdev", "./"+storybookDirectory)
	command.Dir, command.Stdout, command.Stderr = root, stdout, stderr
	command.Env = append(os.Environ(),
		pwstory.AddressVar+"="+address,
		// The shell a story renders inside links the project's stylesheet, so
		// the harness serves the tree it lives in.
		pwstory.PublicVar+"="+filepath.Join(root, filepath.FromSlash(assets.locations().derived)),
		// A story rendered on its own has no document to link a stylesheet
		// from, so the harness is told which to add. pw resolves them because
		// it knows the configured output and the mount it is served at.
		pwstory.StylesVar+"="+strings.Join(styles, " "))
	ownProcessGroup(command)
	if err := command.Start(); err != nil {
		fmt.Fprintln(stderr, "pw dev: storybook:", err)
		return
	}
	result := make(chan error, 1)
	go func() { result <- command.Wait() }()
	s.command, s.exited = command, result
	s.address.Store(&address)
}

// stop ends the running harness, if any, and takes the pane back to reporting
// its own absence.
func (s *devStorybook) stop() {
	if s == nil || s.command == nil {
		return
	}
	stopCommand(s.command, s.exited)
	s.command, s.exited = nil, nil
	s.address.Store(nil)
}

// reserveLoopbackPort picks a free port for the harness.
//
// The harness needs an address before it is started, and pw needs the same one
// to proxy to. Binding and releasing is the ordinary way to be told a free
// number; the window between release and the harness binding is a race nothing
// on a development loopback interface is competing for.
func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
