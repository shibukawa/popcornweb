package pw

import (
	"bufio"
	"context"
	"errors"
	"github.com/shibukawa/popcornweb/pwbrowser"
	"iter"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// livePage builds the shape generation emits for a component whose await clause
// binds a live source: the same boundary, with a binding that keeps delivering.
func livePage(source func(context.Context) iter.Seq2[string, error]) htmlbind.Fragment {
	return livePlan(source, "<main>", "</main>").Bind(struct{}{})
}

func livePlan(source func(context.Context) iter.Seq2[string, error], open, close string) *htmlbind.Plan[struct{}] {
	outer := htmlbind.Builder[struct{}]{}
	return &htmlbind.Plan[struct{}]{
		HasAwaitBlock: true,
		HasLiveBlock:  true,
		Ops: []htmlbind.Op[struct{}]{
			outer.Static(open),
			liveOp(source),
			outer.Static(close),
		},
	}
}

func liveOp(source func(context.Context) iter.Seq2[string, error]) htmlbind.Op[struct{}] {
	outer := htmlbind.Builder[struct{}]{}
	primary := htmlbind.Builder[string]{}
	return outer.Live(
		func(ctx context.Context, _ struct{}) []htmlbind.LiveBinding[string] {
			return []htmlbind.LiveBinding[string]{
				func(deliver func(func(*string), error) bool) error {
					for value, err := range source(ctx) {
						delivered := value
						if !deliver(func(scope *string) { *scope = delivered }, err) {
							return nil
						}
					}
					return nil
				},
			}
		},
		func(struct{}) string { return "" },
		func(_ struct{}, err AsyncError) AsyncError { return err },
		[]htmlbind.Op[string]{
			primary.Static("<p>"),
			primary.Text(func(value string) string { return value }),
			primary.Static("</p>"),
		},
		[]htmlbind.Op[struct{}]{outer.Static("<p>waiting</p>")},
		nil,
	)
}

// liveValues yields each item and then ends, which is a source that finishes.
func liveValues(items ...string) func(context.Context) iter.Seq2[string, error] {
	return func(ctx context.Context) iter.Seq2[string, error] {
		return func(yield func(string, error) bool) {
			for _, item := range items {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}

// liveThenQuiet delivers once and then says nothing until its context ends,
// which is the shape of a real watch between events.
func liveThenQuiet(first string) func(context.Context) iter.Seq2[string, error] {
	return func(ctx context.Context) iter.Seq2[string, error] {
		return func(yield func(string, error) bool) {
			if !yield(first, nil) {
				return
			}
			<-ctx.Done()
		}
	}
}

func liveRequest(target string) *http.Request {
	request := browserRequest(target)
	request.Header.Set(ResponseModeHeader, LiveResponseMode)
	return request
}

func recordLines(t *testing.T, body string) []string {
	t.Helper()
	lines := []string{}
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// A document holding a live boundary ends by saying so, because a client that
// cannot tell a finished screen from a continuing one either never updates or
// pays a whole page execution to find out there was nothing to deliver.
func TestStreamedDocumentMarksLiveWorkRemaining(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, browserRequest("/"), livePage(liveThenQuiet("first")))

	body := recorder.Body.String()
	if !strings.Contains(body, `<tb-stream-end state="live"`) {
		t.Fatalf("document does not mark live work remaining:\n%s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "</tb-stream-end>") {
		t.Errorf("the marker is not the last thing written:\n%s", body)
	}
	// The first delivery commits as an ordinary completion, so the first paint
	// shows content rather than a loading state.
	if !strings.Contains(body, "<p>first</p>") {
		t.Errorf("the first delivery did not commit:\n%s", body)
	}
	if recorder.Header().Get("Vary") == "" || !strings.Contains(recorder.Header().Get("Vary"), ResponseModeHeader) {
		t.Errorf("Vary = %q, want the response mode header", recorder.Header().Values("Vary"))
	}
}

// A page whose boundaries all settle says the opposite, so the screen costs no
// speculative request.
func TestStreamedDocumentMarksItselfFinal(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, browserRequest("/"), asyncPage(asyncPageParams{Body: Resolved("ready")}))

	body := recorder.Body.String()
	if !strings.Contains(body, `<tb-stream-end state="final"`) {
		t.Fatalf("document does not mark itself final:\n%s", body)
	}
	for _, header := range recorder.Header().Values("Vary") {
		if strings.Contains(header, ResponseModeHeader) {
			t.Errorf("a page with nothing live varies on the mode header: %q", header)
		}
	}
}

// A response that gave up on its own document says that too: the fallbacks it
// committed are not going to be replaced by it.
func TestStreamedDocumentMarksItselfFailed(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, browserRequest("/"), noRecoverPage(asyncPageParams{Body: Failed[string](errors.New("upstream is down"))}))

	body := recorder.Body.String()
	if !strings.Contains(body, `<tb-stream-end state="failed"`) {
		t.Fatalf("document does not mark itself failed:\n%s", body)
	}
}

// The buffered branch settles its live boundaries in place and writes no
// placeholder, so it carries no marker and stays byte-identical to what a
// crawler received before live boundaries existed.
func TestBufferedDocumentCarriesNoMarker(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := browserRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), HTMLConfig{}))
	WriteHTML(recorder, request, livePage(liveValues("first")))

	body := recorder.Body.String()
	if strings.Contains(body, "tb-stream-end") {
		t.Fatalf("a buffered document carries a marker:\n%s", body)
	}
	if !strings.Contains(body, "<p>first</p>") {
		t.Errorf("the buffered branch did not render the first delivery:\n%s", body)
	}
}

func TestLiveModeStreamsEveryDelivery(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveValues("one", "two", "three")))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != liveMediaType {
		t.Errorf("Content-Type = %q, want %q", got, liveMediaType)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	lines := recordLines(t, recorder.Body.String())
	if len(lines) < 3 {
		t.Fatalf("records = %v", lines)
	}
	if !strings.HasPrefix(lines[0], `{"r":"head"`) {
		t.Errorf("first record = %q, want the open record", lines[0])
	}
	// No document markup reaches the stream: the body is executed and discarded,
	// so a reconnect repaints nothing that was not live.
	body := recorder.Body.String()
	if strings.Contains(body, "<main>") || strings.Contains(body, "tb-boundary") {
		t.Errorf("live response transferred document markup:\n%s", body)
	}
	deliveries := 0
	for _, line := range lines {
		if strings.Contains(line, `"id":"tb-1"`) {
			deliveries++
		}
	}
	if deliveries != 3 {
		t.Errorf("deliveries = %d, want 3 (one per value)", deliveries)
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, `"r":"end"`) || !strings.Contains(last, `"reason":"done"`) {
		t.Errorf("last record = %q, want a done close", last)
	}
}

// A live request for a page with nothing live is answered rather than held
// open, and it is told not to come back.
func TestLiveModeClosesWhenNothingIsLive(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), asyncPage(asyncPageParams{Body: Resolved("ready")}))

	lines := recordLines(t, recorder.Body.String())
	if len(lines) != 1 || !strings.Contains(lines[0], `"reason":"done"`) {
		t.Fatalf("records = %v, want one done close", lines)
	}
}

// Closing at the configured lifetime is what buys back authorization re-checks
// and deploy rollover, so it must not read as a failure: the client is expected
// straight back.
func TestLiveResponseClosesAtItsLifetimeForRetry(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	config.LiveMaxDuration = 40 * time.Millisecond
	config.LiveDurationJitter = 0
	config.LiveIdleTimeout = 0
	request := liveRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))

	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, livePage(liveThenQuiet("first")))

	lines := recordLines(t, recorder.Body.String())
	last := lines[len(lines)-1]
	if !strings.Contains(last, `"reason":"retry"`) {
		t.Fatalf("last record = %q, want a retry close", last)
	}
	if !strings.Contains(last, `"retryMs":`) {
		t.Errorf("a retry close carries no delay hint: %q", last)
	}
}

// A response nothing is delivering on is holding a goroutine, a source, and a
// connection for a screen that is learning nothing.
func TestLiveResponseClosesWhenIdle(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	config.LiveMaxDuration = 0
	config.LiveIdleTimeout = 40 * time.Millisecond
	request := liveRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))

	recorder := httptest.NewRecorder()
	start := time.Now()
	WriteHTML(recorder, request, livePage(liveThenQuiet("first")))

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("idle response stayed open for %s", elapsed)
	}
	lines := recordLines(t, recorder.Body.String())
	last := lines[len(lines)-1]
	if !strings.Contains(last, `"reason":"retry"`) {
		t.Fatalf("last record = %q, want a retry close", last)
	}
}

// Reaching the boundary bound is reported and closes the response, because a
// screen quietly missing one panel's updates is worse than one that stops.
func TestLiveResponseStopsAtItsBoundaryBound(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	config.LiveMaxBoundaries = 1
	request := liveRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))

	outer := htmlbind.Builder[struct{}]{}
	plan := &htmlbind.Plan[struct{}]{
		HasAwaitBlock: true,
		HasLiveBlock:  true,
		Ops: []htmlbind.Op[struct{}]{
			outer.Static("<main>"),
			liveOp(liveThenQuiet("left")),
			liveOp(liveThenQuiet("right")),
			outer.Static("</main>"),
		},
	}

	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, plan.Bind(struct{}{}))

	lines := recordLines(t, recorder.Body.String())
	ids := map[string]bool{}
	for _, line := range lines {
		if strings.Contains(line, `"id":"tb-1"`) {
			ids["tb-1"] = true
		}
		if strings.Contains(line, `"id":"tb-2"`) {
			ids["tb-2"] = true
		}
	}
	if len(ids) != 1 {
		t.Fatalf("served %d boundaries under a bound of 1: %v", len(ids), lines)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, `"r":"end"`) {
		t.Errorf("last record = %q, want a close", last)
	}
}

// A bound on one response buys nothing against a client that opens ten.
func TestLiveResponsesAreBoundedPerClient(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	config.LiveMaxResponses = 1
	config.LiveMaxDuration = 0
	config.LiveIdleTimeout = 0

	held, holding := context.WithCancel(context.Background())
	defer holding()
	var open sync.WaitGroup
	open.Add(1)
	var first sync.WaitGroup
	first.Add(1)
	go func() {
		defer first.Done()
		request := liveRequest("/")
		ctx := withTestHTMLConfig(held, config)
		request = request.WithContext(ctx)
		recorder := httptest.NewRecorder()
		WriteHTML(recorder, request, livePage(func(ctx context.Context) iter.Seq2[string, error] {
			return func(yield func(string, error) bool) {
				if !yield("first", nil) {
					return
				}
				open.Done()
				<-ctx.Done()
			}
		}))
	}()
	open.Wait()

	request := liveRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, livePage(liveThenQuiet("second")))

	lines := recordLines(t, recorder.Body.String())
	if len(lines) != 1 || !strings.Contains(lines[0], `"reason":"retry"`) {
		t.Fatalf("records = %v, want one refusal asking for a retry", lines)
	}
	holding()
	first.Wait()
}

// Live delivery depends on the placeholders only the streaming branch writes,
// so disabling streaming disables it rather than leaving a client connected to
// a stream whose ids address nothing.
func TestStreamingDisabledRefusesLiveMode(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	config.Streaming = false
	request := liveRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))

	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, livePage(liveThenQuiet("first")))

	lines := recordLines(t, recorder.Body.String())
	if len(lines) != 1 || !strings.Contains(lines[0], `"reason":"done"`) {
		t.Fatalf("records = %v, want one done close", lines)
	}
}

// The runtime and the framing are one design, so the script has to carry the
// half that reads these records.
func TestRuntimeScriptCarriesTheLiveHalf(t *testing.T) {
	for _, fragment := range []string{
		`customElements.define("tb-stream-end"`,
		"Pw-Response-Mode",
		"export function stopLive",
		// Completion is decided from readyState and the marker together: one
		// says when the question can be answered, the other what the answer is.
		// Keying on DOMContentLoaded instead would ask before the subresources
		// a complete document waits for, and keying on the marker alone cannot
		// tell a truncated document from one still arriving.
		`document.addEventListener("readystatechange"`,
		`document.readyState !== "complete"`,
	} {
		if !strings.Contains(boundaryRuntimeScript, fragment) {
			t.Errorf("the runtime script does not carry %q", fragment)
		}
	}
}

// A delivery has to reach the client while the source is still producing.
// A recorder cannot show that: only a real connection can say whether the
// records were flushed as they were written or held until the response ended.
func TestLiveDeliveriesArriveWhileTheSourceIsStillProducing(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteHTML(w, r, livePage(func(ctx context.Context) iter.Seq2[string, error] {
			return func(yield func(string, error) bool) {
				if !yield("first", nil) {
					return
				}
				select {
				case <-release:
				case <-ctx.Done():
					return
				}
				yield("second", nil)
			}
		}))
	}))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("User-Agent", chromeUserAgent)
	request.Header.Set(ResponseModeHeader, LiveResponseMode)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	reader := bufio.NewReader(response.Body)
	// The open record and the first delivery both arrive while the source is
	// blocked, so reading them proves nothing waited for the stream to end.
	for _, want := range []string{`"r":"head"`, `"id":"tb-1"`} {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading %s: %v", want, err)
		}
		if !strings.Contains(line, want) {
			t.Fatalf("record = %q, want one containing %s", line, want)
		}
	}
	if !strings.Contains(response.Header.Get("Content-Type"), liveMediaType) {
		t.Errorf("Content-Type = %q", response.Header.Get("Content-Type"))
	}
}

// A subscription must not outlive the client that opened it: the source is a
// goroutine, and the only thing that can stop an endless one is the context it
// was given.
func TestLiveSourceStopsWhenTheClientDisconnects(t *testing.T) {
	observed := make(chan struct{})
	served := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(served)
		WriteHTML(w, r, livePage(func(ctx context.Context) iter.Seq2[string, error] {
			return func(yield func(string, error) bool) {
				if !yield("first", nil) {
					return
				}
				<-ctx.Done()
				close(observed)
			}
		}))
	}))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("User-Agent", chromeUserAgent)
	request.Header.Set(ResponseModeHeader, LiveResponseMode)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("the source never observed the disconnect")
	}
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never returned")
	}
}

// Defining a custom element upgrades the ones the parser already inserted,
// synchronously, inside the define call. A callback that runs there and reads a
// module binding declared further down the script reads it before its
// initializer has run and throws — which is silent, because it happens during
// load and leaves a page that simply never updates.
//
// The document marker is always already in the DOM when its element is defined,
// so this is the ordinary path rather than a race, and the ordering is worth a
// test rather than a comment alone.
func TestRuntimeScriptDeclaresItsStateBeforeDefiningElements(t *testing.T) {
	firstDefine := strings.Index(boundaryRuntimeScript, "customElements.define(")
	if firstDefine < 0 {
		t.Fatal("the runtime script defines no custom element")
	}
	// Both binding forms, because both sit in the temporal dead zone until their
	// initializer runs. Checking only let left the hole a const registry declared
	// below the definitions fell straight through, and the symptom would have
	// been a load-time throw with a page that applies its boundaries and then
	// never updates.
	//
	// Function declarations are exempt: they hoist initialized, so a callback
	// reaching one that appears later in the module still finds it.
	for _, form := range []string{"\nlet ", "\nconst "} {
		for offset := firstDefine; ; {
			index := strings.Index(boundaryRuntimeScript[offset:], form)
			if index < 0 {
				break
			}
			offset += index + 1
			line := boundaryRuntimeScript[offset:]
			if end := strings.IndexByte(line, '\n'); end >= 0 {
				line = line[:end]
			}
			t.Errorf("module state is declared after the first customElements.define: %q", line)
		}
	}
}

// documentManifest returns the validators a streamed document handed the live
// connection it invites, as the marker spells them.
func documentManifest(t *testing.T, body string) string {
	t.Helper()
	const attribute = ` manifest="`
	start := strings.Index(body, attribute)
	if start < 0 {
		return ""
	}
	rest := body[start+len(attribute):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("the manifest attribute is unterminated:\n%s", body)
	}
	return rest[:end]
}

func liveRequestHolding(target, manifest string) *http.Request {
	request := liveRequest(target)
	if manifest != "" {
		request.Header.Set(LiveManifestHeader, manifest)
	}
	return request
}

// deliveredFragment is one fragment as a record spells it. AppendJSON escapes
// for a script context as well as a JSON one, so the markup in a record is not
// the markup a template wrote and a test comparing the two would only prove
// that.
func deliveredFragment(html string) string {
	return string(htmlbind.JSONString(html))
}

// deliveryRecords returns the delivery lines of a live response, dropping the
// head and terminator records that frame them.
func deliveryRecords(t *testing.T, body string) []string {
	t.Helper()
	deliveries := []string{}
	for _, line := range recordLines(t, body) {
		if strings.Contains(line, `"r":"await"`) {
			deliveries = append(deliveries, line)
		}
	}
	return deliveries
}

// A delivery carries the validator of what it just put on screen, because the
// client cannot compute one and the next connection has to claim it.
func TestLiveDeliveryCarriesItsValidator(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveValues("one")))

	deliveries := deliveryRecords(t, recorder.Body.String())
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1:\n%s", len(deliveries), recorder.Body.String())
	}
	if !strings.Contains(deliveries[0], `"v":"`) {
		t.Errorf("the delivery carries no validator: %s", deliveries[0])
	}
	if !strings.Contains(deliveries[0], `"html":`) {
		t.Errorf("the validator replaced the fragment rather than joining it: %s", deliveries[0])
	}
}

// A source that produces the same value twice costs one transfer. The client
// would discard the second record on arrival, so sending it buys nothing but
// the bandwidth.
func TestLiveSuppressesARepeatedValue(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveValues("one", "one", "two", "two")))

	deliveries := deliveryRecords(t, recorder.Body.String())
	if len(deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2 — one per distinct value:\n%s",
			len(deliveries), recorder.Body.String())
	}
	if !strings.Contains(deliveries[0], deliveredFragment("<p>one</p>")) ||
		!strings.Contains(deliveries[1], deliveredFragment("<p>two</p>")) {
		t.Errorf("the wrong deliveries survived:\n%s", strings.Join(deliveries, "\n"))
	}
}

// A streamed document hands the connection it invites the validators of what it
// committed, so the first connection of a page view costs no more than a later
// one. Without it every page load re-transfers its own screen.
func TestStreamedLiveDocumentCarriesItsManifest(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, browserRequest("/"), livePage(liveValues("first")))

	body := recorder.Body.String()
	manifest := documentManifest(t, body)
	if manifest == "" {
		t.Fatalf("a live document carries no manifest:\n%s", body)
	}
	if !strings.HasPrefix(manifest, "tb-1:") {
		t.Errorf("manifest = %q, want the boundary it committed", manifest)
	}
}

// A document nothing follows carries none. It would be bytes describing a
// conversation that is not going to happen, on every page a project serves.
func TestStreamedFinalDocumentCarriesNoManifest(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, browserRequest("/"), asyncPage(asyncPageParams{Body: Resolved("ready")}))

	if manifest := documentManifest(t, recorder.Body.String()); manifest != "" {
		t.Errorf("a final document carries a manifest: %q", manifest)
	}
}

// The whole point, end to end: a screen that reconnects holding what the
// document gave it is sent nothing it already has.
func TestLiveManifestSuppressesWhatTheScreenAlreadyHolds(t *testing.T) {
	document := httptest.NewRecorder()
	WriteHTML(document, browserRequest("/"), livePage(liveValues("first")))
	manifest := documentManifest(t, document.Body.String())
	if manifest == "" {
		t.Fatalf("the document handed the connection nothing:\n%s", document.Body.String())
	}

	reconnect := httptest.NewRecorder()
	WriteHTML(reconnect, liveRequestHolding("/", manifest), livePage(liveValues("first")))

	if deliveries := deliveryRecords(t, reconnect.Body.String()); len(deliveries) != 0 {
		t.Errorf("a reconnect re-sent what the screen was showing:\n%s", strings.Join(deliveries, "\n"))
	}
	// The stream still opens and still closes, because a client that received
	// no records must still learn whether to come back.
	if !strings.Contains(reconnect.Body.String(), `"r":"head"`) {
		t.Error("a fully suppressed response wrote no opening record")
	}
	if !strings.Contains(reconnect.Body.String(), `"r":"end"`) {
		t.Error("a fully suppressed response wrote no terminal record")
	}
}

// A claim that does not match is not a claim. The server rendered different
// bytes, so the region is stale and the delivery goes out.
func TestLiveManifestDeliversWhatChanged(t *testing.T) {
	document := httptest.NewRecorder()
	WriteHTML(document, browserRequest("/"), livePage(liveValues("first")))
	manifest := documentManifest(t, document.Body.String())

	reconnect := httptest.NewRecorder()
	WriteHTML(reconnect, liveRequestHolding("/", manifest), livePage(liveValues("second")))

	deliveries := deliveryRecords(t, reconnect.Body.String())
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1:\n%s", len(deliveries), reconnect.Body.String())
	}
	if !strings.Contains(deliveries[0], deliveredFragment("<p>second</p>")) {
		t.Errorf("the changed region was not delivered: %s", deliveries[0])
	}
}

// The header is an optimization and every value in it is a claim. A forged or
// mangled one can only cost a delivery that was going to be sent, so nothing
// here refuses a request — a proxy that rewrites headers must not become an
// outage.
func TestLiveManifestToleratesRubbish(t *testing.T) {
	for _, manifest := range []string{
		"",
		"tb-1",
		":",
		"tb-1:",
		":deadbeef",
		",,,",
		"tb-1:not-the-digest",
		strings.Repeat("tb-9:aaaaaaaaaaaaaaaa,", 500),
	} {
		recorder := httptest.NewRecorder()
		WriteHTML(recorder, liveRequestHolding("/", manifest), livePage(liveValues("one")))

		if recorder.Code != http.StatusOK {
			t.Fatalf("manifest %q: status = %d", manifest, recorder.Code)
		}
		if deliveries := deliveryRecords(t, recorder.Body.String()); len(deliveries) != 1 {
			t.Errorf("manifest %q: deliveries = %d, want the unclaimed boundary delivered",
				manifest, len(deliveries))
		}
	}
}

// A claim bounded by what the response can serve. A request naming more
// boundaries than a live response may ever open is claiming about nothing, and
// parsing it in full is work an attacker chooses the size of.
func TestLiveManifestParseIsBounded(t *testing.T) {
	entries := []string{}
	for index := 0; index < 200; index++ {
		entries = append(entries, "tb-"+strconv.Itoa(index)+":aaaaaaaaaaaaaaaa")
	}
	held := parseLiveManifest(strings.Join(entries, ","), []byte("key"), 32)
	if len(held) != 32 {
		t.Errorf("held = %d entries, want the bound of 32", len(held))
	}
	if held := parseLiveManifest("tb-1:aaaa", nil, 32); len(held) != 0 {
		t.Errorf("a process with no key parsed %d entries, want none", len(held))
	}
}

// Suppression is off rather than unkeyed where no key exists, because an
// unkeyed digest in a request header is a stable fingerprint of the region's
// content and request headers are what gets logged.
func TestLiveDigestIsAbsentWithoutAKey(t *testing.T) {
	if digest := liveDigest(nil, []byte("<p>one</p>")); digest != "" {
		t.Errorf("digest = %q with no key, want none", digest)
	}
	keyed := liveDigest([]byte("key"), []byte("<p>one</p>"))
	if keyed == "" {
		t.Fatal("a keyed digest is empty")
	}
	if same := liveDigest([]byte("other"), []byte("<p>one</p>")); same == keyed {
		t.Error("two keys produced one digest")
	}
	if same := liveDigest([]byte("key"), []byte("<p>two</p>")); same == keyed {
		t.Error("two renderings produced one digest")
	}
}

// The configured update key wins, so a reconnect landing on another instance of
// one deployment still compares. Without one the process key stands in, and the
// suppression narrows to a reconnect that returns to the same process.
func TestLiveDigestKeyPrefersTheConfiguredOne(t *testing.T) {
	// A key with enough material is used verbatim. This one is not valid base64
	// (the '!' is outside every alphabet), so it is measured as its raw bytes.
	const longKey = "a-very-long-live-digest-validator-key!"
	configured := liveDigestKey(HTMLConfig{Update: HTMLUpdateConfig{ValidatorKey: longKey}})
	if string(configured) != longKey {
		t.Errorf("key = %q, want the configured one", configured)
	}
	first := liveDigestKey(HTMLConfig{})
	second := liveDigestKey(HTMLConfig{})
	if len(first) == 0 {
		t.Fatal("no fallback key")
	}
	if string(first) != string(second) {
		t.Error("the fallback key differs between calls in one process")
	}
	if string(first) == longKey {
		t.Error("the fallback key is the configured one")
	}
	// A configured key too short to key anything is treated as absent and falls
	// back to the random per-process key rather than keying digests weakly, the
	// same floor validateUpdateConfig enforces at startup.
	short := liveDigestKey(HTMLConfig{Update: HTMLUpdateConfig{ValidatorKey: "shared"}})
	if string(short) != string(first) {
		t.Errorf("a short key was used instead of the process fallback: %q", short)
	}
}

// The browser half is not exercised by the node harness, which stubs the apply
// core it shares with the update runtime. These hold the two ends of the
// manifest together instead: the header the server reads, the attribute the
// marker writes, and the record field a delivery carries.
func TestBoundaryRuntimeCarriesTheManifestBothWays(t *testing.T) {
	for _, fragment := range []string{
		// The name is a contract with LiveManifestHeader below, and a module
		// script cannot read it off its own tag.
		`const liveManifestHeader = "` + LiveManifestHeader + `";`,
		// Sent on every connection, so a reconnect is told what is on screen.
		"headers[liveManifestHeader] = manifest;",
		// Seeded from the document, so the first connection of a page view is
		// as cheap as a later one.
		`seedManifest(this.getAttribute("manifest"));`,
		// Only a range still in the document is claimed: an enclosing boundary
		// re-rendered takes nested ones with it, and claiming those would leave
		// them empty.
		"if (range.digest && range.end.isConnected)",
	} {
		if !strings.Contains(boundaryRuntimeScript, fragment) {
			t.Errorf("the runtime is missing %q", fragment)
		}
	}
}

// A live delivery whose content reaches a component the document never carried
// needs that component's tags before its markup lands. The navigation delta has
// said so since requirement:delta-head-sync; this path had no channel for it at
// all until the stream moved onto the shared record grammar.
func TestLiveHeadRecordCarriesTheChainsTags(t *testing.T) {
	plan := livePlan(liveValues("one"), "<main>", "</main>")
	plan.Head = []string{`<link rel="stylesheet" href="/live.css">`}
	plan.HeadSources = []string{"LivePage"}

	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), plan.Bind(struct{}{}))

	lines := recordLines(t, recorder.Body.String())
	if len(lines) == 0 {
		t.Fatal("the live response wrote nothing")
	}
	if !strings.HasPrefix(lines[0], `{"r":"head"`) {
		t.Fatalf("first record = %q, want the head record", lines[0])
	}
	if !strings.Contains(lines[0], "/live.css") {
		t.Errorf("the head record carries no tags: %s", lines[0])
	}
}

// A page contributing nothing to the head still opens on the head record, so a
// client has one shape to recognize rather than two.
func TestLiveHeadRecordOpensEvenWithNoTags(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveValues("one")))

	lines := recordLines(t, recorder.Body.String())
	if !strings.HasPrefix(lines[0], `{"r":"head"`) {
		t.Fatalf("first record = %q, want the head record", lines[0])
	}
	if strings.Contains(lines[0], `"head":`) {
		t.Errorf("an empty head still travelled: %s", lines[0])
	}
}

// Both stream shapes are the same framing, and reading them twice is how the
// two come apart. One reader is what the shared apply core is for markup.
func TestBoundaryRuntimeReadsRecordsThroughOneReader(t *testing.T) {
	if !strings.Contains(boundaryRuntimeScript, "export async function* readRecords(") {
		t.Error("the boundary half declares no shared record reader")
	}
	if !strings.Contains(boundaryRuntimeScript, "for await (const record of readRecords(response.body))") {
		t.Error("the live half does not read through the shared reader")
	}
	// Two copies of the buffer-split-parse loop is the thing this removes, so a
	// second one reappearing is what the assertion is really against.
	if count := strings.Count(pwbrowser.RuntimeSource(), "getReader()"); count != 1 {
		t.Errorf("the merged asset calls getReader %d times, want one shared reader", count)
	}
}
