package devconsole

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Pane is one surface on the console. A pane is a page rather than a region of
// a shared document: an embedded third-party renderer ships CSS written to own
// a document, and giving it one costs less than isolating it inside a shell.
type Pane struct {
	// Slug is the first path segment the pane is mounted under, and the id the
	// index links to.
	Slug    string
	Title   string
	Summary string
	// Handler serves the pane below its slug, with the slug already stripped.
	// A nil handler means the pane is disabled.
	Handler http.Handler
	// DisabledBy names the configuration key that would enable a disabled
	// pane. The index says this rather than hiding the pane, so a developer
	// who expected a surface learns why it is not there.
	DisabledBy string
	// Framed wraps the pane in a page carrying the console navigation, with the
	// pane itself in an iframe.
	//
	// A pane the console renders can carry the nav directly, and does. A pane it
	// does not render cannot: the telemetry viewer is a browser application with
	// its own document, so navigating to it left the developer inside a page
	// with no way back. A frame is what puts the nav above a document the
	// console does not own, and it is worth the sizing it costs only for that
	// case.
	Framed bool
	// RootPaths are absolute console paths this pane owns outside its own
	// subtree.
	//
	// This exists for one reason. The telemetry UI is a committed build whose
	// bundle resolves its API against the document origin rather than against
	// its own base, so mounting the page under a prefix does not move its
	// fetches with it. Teaching it otherwise means rebuilding the bundle,
	// which needs the Node toolchain the embedded build exists to avoid.
	RootPaths []string
}

// Enabled reports whether the pane has a handler. It is exported because the
// index template asks each pane whether to link it or explain it.
func (p Pane) Enabled() bool { return p.Handler != nil }

// Project is what the index says about the project itself.
type Project struct {
	Name string
	// Environment is the APP_ENV the loop runs the application under.
	Environment string
	// ApplicationURL is where the application listens, or empty when pw could
	// not determine it. The index reports an undetermined value as
	// undetermined rather than printing a default that may be wrong.
	ApplicationURL string
	// APIDocURL is the documentation UI the application already serves, at the
	// path its configuration puts it. The console links it rather than
	// rendering the specification itself: a second renderer would be a second
	// thing to keep current with the same document, and a link cannot disagree
	// with what the application serves.
	//
	// Empty means the endpoint is off, or that its path could not be resolved.
	APIDocURL string
	// APIDocKey names the configuration key that turns the endpoint on, for the
	// index to quote when there is no URL to link.
	APIDocKey string
}

// Console is the listener, the panes mounted on it, and the current loop state.
type Console struct {
	listener net.Listener
	server   *http.Server
	state    *stateHolder
	project  Project
	panes    []Pane
	// attach holds what the application announced about itself, and the token
	// an announcement has to carry. It is created before the console, because
	// the pane that uses it is one of the panes the console is built with.
	attach *Attachment
	// reseed runs the project's seed datasets. It is held as a function rather
	// than implemented here because seeding is already a pw subcommand, and
	// policy:dev-console-boundary admits an action only where one exists.
	reseed atomic.Pointer[func(context.Context) error]
}

// SetReseed installs the action behind the index's reseed button. Nothing is
// offered until it is set, so a project with no datasets shows no button.
func (c *Console) SetReseed(action func(context.Context) error) {
	if c != nil && action != nil {
		c.reseed.Store(&action)
	}
}

// CanReseed reports whether the action is available, for the index to decide
// whether to offer it.
func (c *Console) CanReseed() bool { return c != nil && c.reseed.Load() != nil }

// runReseed applies the seed datasets and returns to the index.
//
// Seeding is clear-insert, so this truncates the tables its datasets target.
// That is what makes it the undo for an editing session, and why the button
// says so rather than being labelled as a refresh.
func (c *Console) runReseed(w http.ResponseWriter, r *http.Request) {
	action := c.reseed.Load()
	if action == nil {
		http.Error(w, "this project has no seed datasets", http.StatusNotFound)
		return
	}
	target := "/"
	if err := (*action)(r.Context()); err != nil {
		target += "?error=" + url.QueryEscape(err.Error())
	} else {
		target += "?seeded=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// Attachment is what the application published about itself — the address of a
// pane it serves and the URL it listens on — plus the per-run token that guards
// both.
//
// The application dials out to announce; the console never dials in. That is
// what keeps a development pane off the application's own listener while still
// letting one page reach it, and it is the direction the telemetry exporter
// already uses.
type Attachment struct {
	token     string
	address   atomic.Pointer[string]
	listening atomic.Pointer[string]
}

// NewAttachment prepares one. An empty token accepts no announcement, which is
// what a loop that generated none should do.
func NewAttachment(token string) *Attachment {
	return &Attachment{token: token}
}

// Address is where the application says it is listening, or empty before it has
// said so.
func (a *Attachment) Address() string {
	if a == nil {
		return ""
	}
	if address := a.address.Load(); address != nil {
		return *address
	}
	return ""
}

// Listening is the URL the application says it bound, or empty before it has
// said so.
func (a *Attachment) Listening() string {
	if a == nil {
		return ""
	}
	if listening := a.listening.Load(); listening != nil {
		return *listening
	}
	return ""
}

// Handler proxies a pane to the attached application.
//
// The pane exists before the application does and outlives every restart, so
// this is an indirection over an address filled in later. A pane whose
// application is down says so rather than disappearing from the console.
func (a *Attachment) Handler(what string) http.Handler {
	if a == nil {
		return nil
	}
	return &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			address := a.Address()
			if address == "" {
				// Rewrite cannot fail, so an unset address is sent somewhere
				// unresolvable and picked up by ErrorHandler.
				address = "application.invalid"
			}
			request.SetURL(&url.URL{Scheme: "http", Host: address})
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "the application is not attached, so "+what+" cannot be reached. "+
				"This pane is served by the application itself, because the development database "+
				"is only addressable from inside it. The console index says what the loop is doing.",
				http.StatusServiceUnavailable)
		},
	}
}

// announce records the address of the pane the application serves itself.
//
// The body is an address and nothing else, so there is no shape for a request
// to carry anything the console then has to decide about.
func (c *Console) announce(w http.ResponseWriter, r *http.Request) {
	address, ok := c.announced(w, r)
	if !ok {
		return
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		http.Error(w, "not an address", http.StatusBadRequest)
		return
	}
	c.attach.address.Store(&address)
	w.WriteHeader(http.StatusNoContent)
}

// announceListening records the URL the application says it bound.
//
// The console can otherwise only derive one by reading the project's
// development configuration, which is a guess: an environment variable or a
// flag outranks the file, and a development run shifts off a port it cannot
// bind. The link on the index is worth nothing if it opens whatever else holds
// the configured port, so the process that owns the listener gets to say.
//
// The body is one URL and nothing else, and it has to be an HTTP one: the value
// is rendered into a link, and a scheme is the part of a URL that decides what
// following it does.
func (c *Console) announceListening(w http.ResponseWriter, r *http.Request) {
	listening, ok := c.announced(w, r)
	if !ok {
		return
	}
	parsed, err := url.Parse(listening)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		http.Error(w, "not an http URL", http.StatusBadRequest)
		return
	}
	c.attach.listening.Store(&listening)
	w.WriteHeader(http.StatusNoContent)
}

// announced authenticates one announcement and returns its body. It writes the
// refusal itself, so a caller that gets false has nothing left to do.
func (c *Console) announced(w http.ResponseWriter, r *http.Request) (string, bool) {
	if c.attach == nil || c.attach.token == "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Pw-Attach-Token")), []byte(c.attach.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 256))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	return strings.TrimSpace(string(body)), true
}

// New binds the console listener and serves the index and every pane on it.
//
// The address is fixed by configuration rather than reserved, so a bound port
// is a real failure with a real remedy rather than something to route around.
func New(address string, project Project, panes []Pane, attach *Attachment) (*Console, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", address, err)
	}
	sort.SliceStable(panes, func(i, j int) bool { return panes[i].Slug < panes[j].Slug })
	console := &Console{
		listener: listener,
		state:    &stateHolder{},
		project:  project,
		panes:    panes,
		attach:   attach,
	}
	console.server = &http.Server{Handler: console.routes()}
	go console.server.Serve(listener)
	return console, nil
}

func (c *Console) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", c.index)
	mux.HandleFunc("GET /api/loop-state", c.loopState)
	mux.HandleFunc("GET /api/loop-state/stream", c.loopStateStream)
	mux.HandleFunc("GET /api/application", c.application)
	mux.HandleFunc("POST /api/attach", c.announce)
	mux.HandleFunc("POST /api/listening", c.announceListening)
	mux.HandleFunc("POST /api/reseed", c.runReseed)
	for _, pane := range c.panes {
		if !pane.Enabled() {
			continue
		}
		prefix := "/" + pane.Slug
		if pane.Framed {
			// The frame takes the pane's own address, so the nav and every link
			// to it are unchanged, and the pane moves one segment deeper. It
			// cannot share the address: a file server canonicalises a request
			// for index.html into a redirect to the directory, which would land
			// back on the frame and load it inside itself.
			inner := prefix + framedSuffix
			mux.Handle("GET "+prefix+"/{$}", c.framedPane(pane, inner+"/"))
			mux.Handle(inner+"/", http.StripPrefix(inner, c.withPane(inner, pane.Handler)))
			continue
		}
		mux.Handle(prefix+"/", http.StripPrefix(prefix, c.withPane(prefix, pane.Handler)))
		// A pane reached without its trailing slash would resolve its own
		// relative asset references against the console root, so the redirect
		// is what keeps a hand-typed URL working.
		mux.Handle("GET "+prefix, http.RedirectHandler(prefix+"/", http.StatusMovedPermanently))
		for _, path := range pane.RootPaths {
			mux.Handle(path, pane.Handler)
		}
	}
	return secureConsole(mux)
}

// secureConsole keeps the loopback listener from becoming a browser-reachable
// authority for an unrelated web page.
//
// Binding 127.0.0.1 keeps remote TCP clients out, but it does not keep a page
// open in the developer's browser from submitting a form to this known port.
// Nor does it stop a DNS-rebinding name from resolving to loopback and making
// its Origin agree with its attacker-controlled Host. The console can reseed a
// database and its panes can edit one, so both checks belong in front of every
// mounted handler rather than in the actions known today.
func secureConsole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Panes are framed by the console itself, so SAMEORIGIN rather than DENY
		// prevents an unrelated page from clickjacking an action without breaking
		// the data and telemetry panes.
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if !loopbackHost(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !consoleSafeMethod(r.Method) && !announcementPath(r.URL.Path) &&
			browserOriginRequired(r) && !sameConsoleOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func consoleSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// Announcements are service-to-service requests carrying a per-run secret and
// no browser Origin. Their own authentication is stronger than the browser
// comparison and remains the only exception to it.
func announcementPath(path string) bool {
	return path == "/api/attach" || path == "/api/listening"
}

// browserOriginRequired distinguishes a browser action from a service POST.
// The telemetry pane is also an OTLP receiver, so its JSON and protobuf POSTs
// carry neither Origin nor Referer and must remain usable. A browser cannot
// send those media types cross-origin without a preflight; the eventual POST
// carries Origin and is refused. The three form-capable media types are CORS
// safelisted and need the comparison even when an older browser omits Origin.
func browserOriginRequired(r *http.Request) bool {
	if r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" {
		return true
	}
	mediaType, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), ";")
	mediaType = strings.TrimSpace(mediaType)
	switch mediaType {
	case "", "application/x-www-form-urlencoded", "multipart/form-data", "text/plain":
		return true
	}
	return false
}

// sameConsoleOrigin accepts an unsafe browser request only from the console
// page that owns this Host. Origin is authoritative when present; Referer is a
// fallback for clients that omit it. browserOriginRequired decides whether an
// absent pair is a browser form to refuse or a non-browser service request.
func sameConsoleOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		return err == nil && parsed.Scheme == "http" && parsed.User == nil && parsed.Opaque == "" &&
			parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" &&
			strings.EqualFold(parsed.Host, r.Host)
	}
	referer := r.Header.Get("Referer")
	if referer == "" {
		return false
	}
	parsed, err := url.Parse(referer)
	return err == nil && parsed.Scheme == "http" && parsed.User == nil && parsed.Opaque == "" &&
		strings.EqualFold(parsed.Host, r.Host)
}

// PanePrefixHeader tells a pane where it is mounted.
//
// A pane serves itself and cannot know: the console strips its prefix before the
// request arrives, so a link the pane writes as an absolute path resolves
// against the console root and misses. Rather than have every pane guess, the
// mount says so, and a pane that sees this header prefixes its own links and
// offers the way back to the console.
const PanePrefixHeader = "X-Pw-Pane-Prefix"

// framedSuffix is where a framed pane actually answers, one segment below the
// address the frame occupies.
const framedSuffix = "/pane"

// framedPane renders the console navigation with the pane inside a frame.
func (c *Console) framedPane(pane Pane, entry string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.renderFramed(w, pane.Slug, pane.Title, buildHTML(framePage, framedData{
			Slug: pane.Slug, Title: pane.Title, Summary: pane.Summary, Entry: entry,
		}))
	})
}

// withPane tells the pane its mount and hands it the console navigation.
func (c *Console) withPane(prefix string, handler http.Handler) http.Handler {
	inner := c.withNav(handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(PanePrefixHeader, prefix)
		inner.ServeHTTP(w, r)
	})
}

// withNav hands a pane the console navigation. A pane that renders with the
// console layout needs the same nav as every other page, and passing it through
// the request keeps a pane constructor from taking a Console that does not exist
// yet when the pane is built.
func (c *Console) withNav(handler http.Handler) http.Handler {
	nav := c.nav()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), navKey{}, nav)))
	})
}

func (c *Console) nav() []navPane {
	nav := make([]navPane, 0, len(c.panes))
	for _, pane := range c.panes {
		nav = append(nav, navPane{Slug: pane.Slug, Title: pane.Title, Enabled: pane.Enabled()})
	}
	return nav
}

// URL is the one address api:cli-dev prints for the console.
// projectView is what a page should say about the project now.
//
// The application's address is the one it announced when it has announced one,
// because everything else here was read from a project file that an environment
// variable, a flag, or a development port shift outranks. The documentation link
// moves with it: that page is a path on the application's own origin, so a link
// left behind on the configured port opens whatever else now holds it.
func (c *Console) projectView() Project {
	project := c.project
	listening := c.attach.Listening()
	if listening == "" {
		return project
	}
	project.ApplicationURL, project.APIDocURL = listening, rehost(project.APIDocURL, listening)
	return project
}

// rehost keeps a URL's path and moves it onto the origin the application
// announced. An unreadable value is dropped rather than linked, because a link
// that goes nowhere is worse than the sentence saying there is none.
func rehost(link, origin string) string {
	if link == "" {
		return ""
	}
	target, err := url.Parse(link)
	if err != nil {
		return ""
	}
	base, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	target.Scheme, target.Host = base.Scheme, base.Host
	return target.String()
}

func (c *Console) URL() string {
	if c == nil {
		return ""
	}
	return "http://" + c.listener.Addr().String()
}

// Publish records a loop transition. It is safe on a nil console, because a
// console that failed to listen must not turn every phase change into a branch
// at the call site.
func (c *Console) Publish(phase string, status Status, diagnostic *Diagnostic) {
	if c == nil {
		return
	}
	c.state.publish(phase, status, diagnostic, time.Now())
}

// Failed is the common case of Publish: a phase that produced a diagnostic.
func (c *Console) Failed(phase string, text string) {
	if c == nil || strings.TrimSpace(text) == "" {
		return
	}
	c.Publish(phase, StatusFailed, &Diagnostic{Text: text})
}

// State reports what the console currently holds, for callers that render it
// themselves.
func (c *Console) State() State {
	if c == nil {
		return State{}
	}
	return c.state.get()
}

func (c *Console) loopState(w http.ResponseWriter, r *http.Request) {
	allowLoopbackOrigin(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(c.state.get())
}

// application answers where the application is listening, as it announced it,
// or an empty string before it has. pw request asks this rather than reading
// the project, because the announced address is the only one that survives a
// port shift, and the loop keeps it in memory rather than in a file.
func (c *Console) application(w http.ResponseWriter, r *http.Request) {
	allowLoopbackOrigin(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(ApplicationAddress{Listening: c.attach.Listening()})
}

// ApplicationAddress is the body of GET /api/application.
type ApplicationAddress struct {
	Listening string `json:"listening"`
}

// loopStateStream pushes the current state and then every transition.
//
// The stream terminates here rather than at the application, which is the whole
// point of it: a page keeps being told what the loop is doing while the process
// that served it is stopped, which is exactly when a developer wants to know.
func (c *Console) loopStateStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	allowLoopbackOrigin(w, r)
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-store")
	// The console is reached directly on loopback, but a proxy between them
	// would otherwise buffer the stream into uselessness.
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	woken, stop := c.state.watch()
	defer stop()
	// The first record goes out before anything is awaited, so a page that
	// connects after the transition it cares about is still told about it.
	if !writeStateEvent(w, flusher, c.state.get()) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-woken:
			if !writeStateEvent(w, flusher, c.state.get()) {
				return
			}
		}
	}
}

func writeStateEvent(w http.ResponseWriter, flusher http.Flusher, state State) bool {
	encoded, err := json.Marshal(state)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// allowLoopbackOrigin lets a page served by the application read this response.
//
// The application and the console are two ports, so a page from one is a
// different origin to the other and a plain fetch would be refused. The
// allowance is echoed per request and only for a loopback origin: the console
// binds to loopback anyway, so this widens nothing, and naming the origin back
// rather than answering "*" keeps the response uncacheable across origins.
func allowLoopbackOrigin(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || !loopbackOrigin(origin) {
		return
	}
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", origin)
	header.Add("Vary", "Origin")
}

func loopbackOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return loopbackHost(parsed.Host)
}

// loopbackHost validates the authority rather than resolving it. Resolving an
// arbitrary name and accepting a loopback answer is exactly the DNS-rebinding
// route this boundary must close.
func loopbackHost(authority string) bool {
	if authority == "" || strings.ContainsAny(authority, "/\\@") {
		return false
	}
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		host = authority
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// Close stops the console with the developer loop. Nothing here is persisted,
// so there is nothing to flush.
func (c *Console) Close() {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.server.Shutdown(ctx)
}
