package devconsole

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func startConsole(t *testing.T, panes ...Pane) *Console {
	t.Helper()
	console, err := New("127.0.0.1:0", Project{Name: "app", Environment: "dev"}, panes, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(console.Close)
	return console
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return response.StatusCode, string(body)
}

func TestConsoleServesTheIndexOnALoopbackPort(t *testing.T) {
	console := startConsole(t)
	if !strings.HasPrefix(console.URL(), "http://127.0.0.1:") {
		t.Fatalf("URL = %q, want a loopback address", console.URL())
	}
	status, body := get(t, console.URL()+"/")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(body, "app") {
		t.Errorf("the index never named the project:\n%s", body)
	}
}

func TestIndexNamesADisabledPaneAndTheKeyThatEnablesIt(t *testing.T) {
	console := startConsole(t,
		Pane{Slug: "assets", Title: "assets", DisabledBy: "dev.console.assets.enabled"},
		Pane{Slug: "telemetry", Title: "telemetry", Handler: http.NotFoundHandler()},
	)
	_, body := get(t, console.URL()+"/")
	if !strings.Contains(body, "dev.console.assets.enabled") {
		t.Errorf("a disabled pane was hidden rather than explained:\n%s", body)
	}
	if !strings.Contains(body, `href="/telemetry/"`) {
		t.Errorf("the index never linked the enabled pane:\n%s", body)
	}
}

func TestUndeterminedApplicationURLIsSaidRatherThanGuessed(t *testing.T) {
	console := startConsole(t)
	_, body := get(t, console.URL()+"/")
	if !strings.Contains(body, "undetermined") {
		t.Errorf("an unknown application address was not reported as unknown:\n%s", body)
	}
}

func TestPaneIsMountedUnderItsSlugWithThePrefixStripped(t *testing.T) {
	console := startConsole(t, Pane{Slug: "assets", Title: "assets",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "path="+r.URL.Path)
		})})
	if _, body := get(t, console.URL()+"/assets/"); body != "path=/" {
		t.Errorf("body = %q, want the prefix stripped", body)
	}
	if _, body := get(t, console.URL()+"/assets/deep/file.css"); body != "path=/deep/file.css" {
		t.Errorf("body = %q, want the prefix stripped", body)
	}
}

// A pane's root paths exist for the telemetry bundle, which resolves its API
// against the document origin and so cannot follow its page under a prefix.
func TestPaneRootPathsAreServedAtTheConsoleRoot(t *testing.T) {
	console := startConsole(t, Pane{Slug: "telemetry", Title: "telemetry",
		RootPaths: []string{"/api/snapshot"},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "path="+r.URL.Path)
		})})
	if _, body := get(t, console.URL()+"/api/snapshot"); body != "path=/api/snapshot" {
		t.Errorf("body = %q, want the unstripped root path", body)
	}
}

func TestLoopStateStartsEmptyAndRecordsTransitions(t *testing.T) {
	console := startConsole(t)
	if state := console.State(); state.Phase != "" || state.Status != "" {
		t.Fatalf("state = %+v, want an empty one before any phase", state)
	}
	console.Publish("generating", StatusStarting, nil)
	console.Failed("generating", "handlers/home.go:12:3: undefined: Titel")

	state := console.State()
	if state.Status != StatusFailed {
		t.Errorf("status = %q, want %q", state.Status, StatusFailed)
	}
	if state.Diagnostic == nil || !strings.Contains(state.Diagnostic.Text, "undefined: Titel") {
		t.Fatalf("diagnostic = %+v, want the text unchanged", state.Diagnostic)
	}
	_, body := get(t, console.URL()+"/")
	if !strings.Contains(body, "undefined: Titel") {
		t.Errorf("the index never showed the diagnostic:\n%s", body)
	}
}

func TestHealthyClearsAPreviousDiagnosticAndAdvancesTheBuild(t *testing.T) {
	console := startConsole(t)
	console.Failed("building", "exit status 1")
	console.Publish("running", StatusHealthy, nil)

	first := console.State()
	if first.Diagnostic != nil {
		t.Errorf("diagnostic = %+v, want a healthy transition to clear it", first.Diagnostic)
	}
	if first.Build == "" {
		t.Fatal("a healthy transition left the build identity empty")
	}
	// A restart is what makes an open page stale, so the identity moves on the
	// transition back to healthy and not on every publish.
	console.Publish("running", StatusHealthy, nil)
	if console.State().Build != first.Build {
		t.Error("the build identity moved without a restart")
	}
	console.Failed("building", "exit status 1")
	console.Publish("running", StatusHealthy, nil)
	if console.State().Build == first.Build {
		t.Error("the build identity did not move across a restart")
	}
}

func TestLoopStateIsReadableAsJSON(t *testing.T) {
	console := startConsole(t)
	console.Failed("applying migrations", "no Down for 003_add_index.sql")

	_, body := get(t, console.URL()+"/api/loop-state")
	var state State
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if state.Phase != "applying migrations" || state.Status != StatusFailed {
		t.Errorf("state = %+v, want the failed migration phase", state)
	}
	if state.Diagnostic == nil || state.Diagnostic.Text != "no Down for 003_add_index.sql" {
		t.Errorf("diagnostic = %+v, want the text unchanged", state.Diagnostic)
	}
}

func TestApplicationAddressIsEmptyUntilAnnouncedAndThenTheAnnouncedURL(t *testing.T) {
	attach := NewAttachment("secret")
	console, err := New("127.0.0.1:0", Project{Name: "app", Environment: "dev"}, nil, attach)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(console.Close)

	_, body := get(t, console.URL()+"/api/application")
	var address ApplicationAddress
	if err := json.Unmarshal([]byte(body), &address); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if address.Listening != "" {
		t.Errorf("listening = %q before any announcement", address.Listening)
	}

	request, _ := http.NewRequest(http.MethodPost, console.URL()+"/api/listening", strings.NewReader("http://localhost:8081"))
	request.Header.Set("X-Pw-Attach-Token", "secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("announce: %d", response.StatusCode)
	}
	_, body = get(t, console.URL()+"/api/application")
	if err := json.Unmarshal([]byte(body), &address); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if address.Listening != "http://localhost:8081" {
		t.Errorf("listening = %q, want the announced URL", address.Listening)
	}
}

// A console that could not listen is an ordinary outcome, and every call site
// in the loop would otherwise need a branch for it.
func TestNilConsoleToleratesEveryCall(t *testing.T) {
	var console *Console
	console.Publish("generating", StatusStarting, nil)
	console.Failed("generating", "boom")
	console.Close()
	if console.URL() != "" || console.State().Phase != "" {
		t.Error("a nil console answered as though it were running")
	}
}

// The stream sends what is true now before it waits, so a page that connects
// after the transition it cares about is still told about it.
func TestStreamSendsTheCurrentStateBeforeWaiting(t *testing.T) {
	console := startConsole(t)
	console.Failed("generating", "undefined: Titel")

	state := readStreamEvent(t, console, nil)
	if state.Status != StatusFailed || state.Diagnostic == nil {
		t.Fatalf("first event = %+v, want the failure already recorded", state)
	}
}

func TestStreamPushesLaterTransitions(t *testing.T) {
	console := startConsole(t)
	console.Publish("starting services", StatusStarting, nil)

	state := readStreamEvent(t, console, func() {
		console.Failed("building CSS", "tailwindcss: exit status 1")
	})
	if state.Status != StatusFailed || state.Phase != "building CSS" {
		t.Fatalf("pushed event = %+v, want the CSS failure", state)
	}
}

// A page served by the application is a different origin to the console, so a
// stream it cannot read is a stream that does not work.
func TestStreamAllowsALoopbackOrigin(t *testing.T) {
	console := startConsole(t)
	request, err := http.NewRequest(http.MethodGet, console.URL()+"/api/loop-state/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:8080" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the loopback origin echoed", got)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want an event stream", got)
	}
}

func TestIndexLinksTheAPIDocumentationTheApplicationServes(t *testing.T) {
	console, err := New("127.0.0.1:0", Project{
		Name: "app", Environment: "dev",
		ApplicationURL: "http://localhost:8080",
		APIDocURL:      "http://localhost:8080/reference",
		APIDocKey:      "server.api_doc",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(console.Close)
	_, body := get(t, console.URL()+"/")
	if !strings.Contains(body, "http://localhost:8080/reference") {
		t.Errorf("the index never linked the documentation:\n%s", body)
	}
}

func TestIndexNamesTheKeyWhenTheAPIDocumentationIsOff(t *testing.T) {
	console, err := New("127.0.0.1:0", Project{
		Name: "app", Environment: "dev", APIDocKey: "server.api_doc",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(console.Close)
	_, body := get(t, console.URL()+"/")
	if !strings.Contains(body, "server.api_doc") {
		t.Errorf("the index never named the key that enables it:\n%s", body)
	}
}

// startAttachedConsole runs a console that has an address to guess from and a
// token to check announcements against.
func startAttachedConsole(t *testing.T, token string) *Console {
	t.Helper()
	console, err := New("127.0.0.1:0", Project{
		Name: "app", Environment: "dev",
		ApplicationURL: "http://localhost:8080",
		APIDocURL:      "http://localhost:8080/docs",
		APIDocKey:      "server.api_doc",
	}, nil, NewAttachment(token))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(console.Close)
	return console
}

func announce(t *testing.T, console *Console, token, body string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, console.URL()+"/api/listening", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Pw-Attach-Token", token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("announce: %v", err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

// The address read from the project files is a guess, and a development run that
// could not bind the configured port makes it the wrong one. The link has to
// follow the process rather than the file, or it opens whatever else took 8080.
func TestTheIndexPrefersTheAddressTheApplicationAnnounced(t *testing.T) {
	console := startAttachedConsole(t, "secret")
	if status := announce(t, console, "secret", "http://localhost:8081"); status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	_, body := get(t, console.URL()+"/")
	if !strings.Contains(body, `href="http://localhost:8081"`) {
		t.Errorf("the index never linked the announced address:\n%s", body)
	}
	if strings.Contains(body, "localhost:8080") {
		t.Errorf("the index still shows the address it guessed:\n%s", body)
	}
	// The documentation is a path on the application's own origin, so it moves
	// with it.
	if !strings.Contains(body, "http://localhost:8081/docs") {
		t.Errorf("the documentation link stayed on the configured port:\n%s", body)
	}
}

// Before the application says anything the console still has its guess, which is
// right whenever the port was free.
func TestTheIndexFallsBackToTheConfiguredAddress(t *testing.T) {
	console := startAttachedConsole(t, "secret")
	_, body := get(t, console.URL()+"/")
	if !strings.Contains(body, "http://localhost:8080") {
		t.Errorf("the index dropped the address it could work out:\n%s", body)
	}
}

// The announcement moves where a developer's browser is sent, so reaching the
// port is not enough to make one.
func TestAnAnnouncementWithoutTheTokenIsRefused(t *testing.T) {
	console := startAttachedConsole(t, "secret")
	if status := announce(t, console, "guessed", "http://localhost:9999"); status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if console.attach.Listening() != "" {
		t.Errorf("an unauthenticated announcement was recorded: %s", console.attach.Listening())
	}
}

// The value is rendered into a link, and a scheme is the part of a URL that
// decides what following it does.
func TestOnlyAnHTTPURLIsAccepted(t *testing.T) {
	for _, body := range []string{"javascript:alert(1)", "localhost:8081", "", "file:///etc/passwd"} {
		t.Run(body, func(t *testing.T) {
			console := startAttachedConsole(t, "secret")
			if status := announce(t, console, "secret", body); status != http.StatusBadRequest {
				t.Fatalf("status = %d for %q, want 400", status, body)
			}
		})
	}
}

// readStreamEvent opens the stream, runs during if given, and returns the last
// state the stream delivered.
func readStreamEvent(t *testing.T, console *Console, during func()) State {
	t.Helper()
	response, err := http.Get(console.URL() + "/api/loop-state/stream")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	reader := bufio.NewReader(response.Body)
	read := func() State {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read stream: %v", err)
			}
			payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data: ")
			if !ok {
				continue
			}
			var state State
			if err := json.Unmarshal([]byte(payload), &state); err != nil {
				t.Fatalf("decode %q: %v", payload, err)
			}
			return state
		}
	}
	first := read()
	if during == nil {
		return first
	}
	during()
	return read()
}

// A project with no seed datasets is offered no action, rather than a button
// that fails when pressed.
func TestReseedIsOfferedOnlyWhenAvailable(t *testing.T) {
	console := startConsole(t)
	if console.CanReseed() {
		t.Error("reseed was offered before any action was installed")
	}
	if _, body := get(t, console.URL()+"/"); strings.Contains(body, "reseed") {
		t.Errorf("the index offered reseed with no action:\n%s", body)
	}
	console.SetReseed(func(context.Context) error { return nil })
	if _, body := get(t, console.URL()+"/"); !strings.Contains(body, "reseed") {
		t.Errorf("the index did not offer reseed:\n%s", body)
	}
}

func TestReseedRunsTheActionAndReportsIt(t *testing.T) {
	console := startConsole(t)
	ran := false
	console.SetReseed(func(context.Context) error { ran = true; return nil })

	response, err := postFromConsole(console, "/api/reseed")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !ran {
		t.Error("the reseed action was not run")
	}
	if location := response.Request.URL.Query().Get("seeded"); location == "" {
		// The client follows the redirect, so the landing URL carries the result.
		t.Errorf("landed at %s, want the seeded marker", response.Request.URL)
	}
}

// A failing reseed reports why on the index rather than leaving the developer
// to check the terminal.
func TestReseedReportsAFailure(t *testing.T) {
	console := startConsole(t)
	console.SetReseed(func(context.Context) error { return errors.New("dataset users.yaml: no such table") })

	response, err := postFromConsole(console, "/api/reseed")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "no such table") {
		t.Errorf("the failure was not reported:\n%s", body)
	}
}

func postFromConsole(console *Console, path string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodPost, console.URL()+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Origin", console.URL())
	return http.DefaultClient.Do(request)
}

func TestCrossSitePostCannotRunAConsoleAction(t *testing.T) {
	console := startConsole(t)
	ran := false
	console.SetReseed(func(context.Context) error { ran = true; return nil })

	request, err := http.NewRequest(http.MethodPost, console.URL()+"/api/reseed", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "https://attacker.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.StatusCode)
	}
	if ran {
		t.Fatal("a cross-site request ran the reseed action")
	}
}

func TestRebindingHostCannotReachTheConsole(t *testing.T) {
	console := startConsole(t)
	request, err := http.NewRequest(http.MethodGet, console.URL()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The connection still goes to 127.0.0.1; only the HTTP authority is the
	// attacker-controlled name a rebinding page would use.
	request.Host = "project.attacker.example"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.StatusCode)
	}
}

func TestConsoleResponsesCanOnlyBeFramedByTheConsole(t *testing.T) {
	console := startConsole(t)
	response, err := http.Get(console.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", got)
	}
	if got := response.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
		t.Errorf("Content-Security-Policy = %q", got)
	}
}

// The pane shows what the command said rather than a second rendering of it,
// so a command that exits nonzero still has its output read.
func TestTextPaneShowsOutputEvenWhenTheCommandFails(t *testing.T) {
	pane := TextPane("doctor", "what this environment would run",
		func(context.Context) (string, error) {
			return "findings:\n  error  the database is unreachable", errors.New("1 error")
		})
	recorder := httptest.NewRecorder()
	pane.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, "the database is unreachable") {
		t.Errorf("the output was replaced by the error:\n%s", body)
	}
	if !strings.Contains(body, "1 error") {
		t.Errorf("the failure was not reported beside it:\n%s", body)
	}
}

func TestTextPaneRendersTheOutputUnaltered(t *testing.T) {
	pane := TextPane("doctor", "summary", func(context.Context) (string, error) {
		return "features:\n  database   sqlite\n  sessions   off", nil
	})
	recorder := httptest.NewRecorder()
	pane.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	// The command's own layout is what it spent its effort on, so the pane
	// preserves it rather than reflowing it into a table.
	if body := recorder.Body.String(); !strings.Contains(body, "  database   sqlite\n") {
		t.Errorf("the output was reflowed:\n%s", body)
	}
}

// The application is a separate thing to look at, not a place to navigate the
// console to, so following it keeps the console where it was.
func TestApplicationLinkOpensInItsOwnTab(t *testing.T) {
	console, err := New("127.0.0.1:0", Project{
		Name: "app", Environment: "dev", ApplicationURL: "http://localhost:8080",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(console.Close)
	_, body := get(t, console.URL()+"/")
	if !strings.Contains(body, `href="http://localhost:8080" target="_blank"`) {
		t.Errorf("the application link does not open in its own tab:\n%s", body)
	}
}
