package pwcli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requestFixtureDocument is the document the fixture application serves: one
// JSON-bodied POST, one template GET, and a path under a guard pattern.
const requestFixtureDocument = `{
  "openapi": "3.1.0",
  "paths": {
    "/api/users": {"post": {"operationId": "CreateUser",
      "requestBody": {"content": {"application/json": {"schema": {"type": "object",
        "properties": {"name": {"type": "string"}, "age": {"type": "integer"}}, "required": ["name"]}}}},
      "responses": {"201": {"description": "Created"}}}},
    "/api/users/{id}": {"get": {"operationId": "ShowUser",
      "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "integer"}},
                     {"in": "query", "name": "verbose", "schema": {"type": "boolean"}}],
      "responses": {"200": {"description": "OK"}}}},
    "/account": {"get": {"operationId": "Account", "responses": {"200": {"description": "OK"}}}}
  }
}`

// received is what the fixture application saw, echoed back as JSON.
type received struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Query       string `json:"query"`
	ContentType string `json:"contentType"`
	Body        string `json:"body"`
	Cookie      string `json:"cookie"`
}

func requestFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, requestFixtureDocument)
	})
	echo := func(status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			http.SetCookie(w, &http.Cookie{Name: "seen", Value: "1", Path: "/"})
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(received{
				Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
				ContentType: r.Header.Get("Content-Type"), Body: string(body), Cookie: r.Header.Get("Cookie"),
			})
		}
	}
	mux.HandleFunc("POST /api/users", echo(http.StatusCreated))
	mux.HandleFunc("GET /api/users/{id}", echo(http.StatusOK))
	mux.HandleFunc("GET /account", echo(http.StatusUnauthorized))
	mux.HandleFunc("/", echo(http.StatusNotFound))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// requestProject is a scaffolded project whose development configuration
// names the document and guards one path, with the shell placed inside it.
func requestProject(t *testing.T) string {
	t.Helper()
	root := writeScaffoldedProject(t, initOptions{Name: "fixture", TinyGo: true, Devbox: true, Database: true, Auth: authNone})
	config := filepath.Join(root, "config.dev.toml")
	source, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	extra := "\n[auth]\nenabled = true\nprotection.include = [\"/account\"]\n"
	if !strings.Contains(string(source), "openapi = ") {
		extra = "\n[server]\nopenapi = \"/openapi.json\"\n" + extra
	}
	if err := os.WriteFile(config, append(source, extra...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

func runRequestCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Main(append([]string{"request"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func decodeReport(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var report map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode report %q: %v", stdout, err)
	}
	return report
}

func echoed(t *testing.T, report map[string]any) received {
	t.Helper()
	encoded, _ := json.Marshal(report["body"])
	var got received
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("body %s: %v", encoded, err)
	}
	return got
}

func TestRequestRoutesPairsIntoAJSONBodyForTheMatchedOperation(t *testing.T) {
	requestProject(t)
	server := requestFixtureServer(t)
	code, stdout, stderr := runRequestCommand(t, "--url", server.URL, "--format=json", "/api/users", "-d", "name=Alice", "-d", "age=3")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	report := decodeReport(t, stdout)
	if report["status"] != float64(201) {
		t.Errorf("status = %v", report["status"])
	}
	got := echoed(t, report)
	if got.Method != "POST" || got.ContentType != "application/json" || got.Body != `{"name":"Alice","age":3}` {
		t.Errorf("received %+v", got)
	}
	operation, _ := report["operation"].(map[string]any)
	if operation["operationId"] != "CreateUser" {
		t.Errorf("operation = %v, want CreateUser", report["operation"])
	}
}

func TestRequestFillsThePathSegmentAndTheQueryFromPairs(t *testing.T) {
	requestProject(t)
	server := requestFixtureServer(t)
	code, stdout, stderr := runRequestCommand(t, "--url", server.URL, "--format=json", "ShowUser", "-d", "id=7", "-d", "verbose=true")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got := echoed(t, decodeReport(t, stdout))
	if got.Method != "GET" || got.Path != "/api/users/7" || got.Query != "verbose=true" || got.Body != "" {
		t.Errorf("received %+v", got)
	}
}

func TestRequestSendsWithCurlSemanticsWhenNothingMatches(t *testing.T) {
	requestProject(t)
	server := requestFixtureServer(t)
	code, stdout, stderr := runRequestCommand(t, "--url", server.URL, "--format=json", "/unknown", "-d", "a=b")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	report := decodeReport(t, stdout)
	got := echoed(t, report)
	if got.Method != "POST" || got.ContentType != "application/x-www-form-urlencoded" || got.Body != "a=b" {
		t.Errorf("received %+v", got)
	}
	if report["operation"] != nil || !strings.Contains(report["reason"].(string), "no operation") {
		t.Errorf("operation = %v, reason = %v", report["operation"], report["reason"])
	}
}

func TestRequestListNamesEveryOperationAndMarksTheGuardedOne(t *testing.T) {
	requestProject(t)
	server := requestFixtureServer(t)
	code, stdout, stderr := runRequestCommand(t, "--url", server.URL, "--list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"POST   /api/users  CreateUser", "GET    /api/users/{id}  ShowUser", "id:integer*  path", "verbose:boolean  query", "GET    /account  Account  [protected]"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("list lacks %q:\n%s", want, stdout)
		}
	}
	code, stdout, _ = runRequestCommand(t, "--url", server.URL, "--list", "--format=json")
	if code != 0 {
		t.Fatalf("json list exit %d", code)
	}
	var listing struct {
		Operations []struct {
			ID        string `json:"operationId"`
			Protected bool   `json:"protected"`
		} `json:"operations"`
	}
	if err := json.Unmarshal([]byte(stdout), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Operations) != 3 || !listing.Operations[0].Protected || listing.Operations[0].ID != "Account" {
		t.Errorf("listing = %+v", listing.Operations)
	}
}

func TestRequestRawOutputPrintsTheBodyAndReportsRoutingOnStderr(t *testing.T) {
	requestProject(t)
	server := requestFixtureServer(t)
	code, stdout, stderr := runRequestCommand(t, "--url", server.URL, "-i", "POST", "/api/users", "-d", "name=Alice")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "HTTP 201\n") || !strings.Contains(stdout, "Content-Type: application/json") || !strings.Contains(stdout, `"method":"POST"`) {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "as CreateUser") || !strings.Contains(stderr, "name -> body") {
		t.Errorf("stderr = %q", stderr)
	}
	_, _, silent := runRequestCommand(t, "--url", server.URL, "-s", "POST", "/api/users", "-d", "name=Alice")
	if silent != "" {
		t.Errorf("-s still wrote %q", silent)
	}
}

func TestRequestFailExitsWithTheCurlCode(t *testing.T) {
	requestProject(t)
	server := requestFixtureServer(t)
	code, _, stderr := runRequestCommand(t, "--url", server.URL, "-f", "-s", "/account")
	if code != 22 || !strings.Contains(stderr, "answered 401") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	code, _, _ = runRequestCommand(t, "--url", server.URL, "-s", "/account")
	if code != 0 {
		t.Fatalf("without -f, exit %d", code)
	}
}

func TestRequestRefusesATargetThatIsNotLoopback(t *testing.T) {
	requestProject(t)
	code, _, stderr := runRequestCommand(t, "--url", "http://example.com", "/x")
	if code != 1 || !strings.Contains(stderr, "not loopback") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestRequestUsageErrorsExitTwo(t *testing.T) {
	requestProject(t)
	server := requestFixtureServer(t)
	for _, args := range [][]string{
		{"--url", server.URL},
		{"--url", server.URL, "--bogus", "/x"},
		{"--url", server.URL, "NoSuchOperation"},
		{"--url", server.URL, "--strict", "ShowUser", "-d", "id=1", "-d", "extra=1"},
		{"--url", server.URL, "/api/users/1", "-d", "name=x", "-F", "f=g"},
	} {
		code, _, stderr := runRequestCommand(t, args...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2: %s", args, code, stderr)
		}
	}
}

func TestRequestCookieJarRoundTrips(t *testing.T) {
	root := requestProject(t)
	server := requestFixtureServer(t)
	jar := filepath.Join(root, "cookies.txt")
	if code, _, stderr := runRequestCommand(t, "--url", server.URL, "-s", "-c", jar, "/api/users", "-d", "name=x"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	code, stdout, stderr := runRequestCommand(t, "--url", server.URL, "--format=json", "-b", jar, "ShowUser", "-d", "id=1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := echoed(t, decodeReport(t, stdout)); got.Cookie != "seen=1" {
		t.Errorf("cookie = %q, want the jar replayed", got.Cookie)
	}
}

func TestRequestOutsideAProjectIsRefused(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, stderr := runRequestCommand(t, "--url", "http://localhost:1", "/x")
	if code != 1 || !strings.Contains(stderr, "popcornweb.toml not found") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}
