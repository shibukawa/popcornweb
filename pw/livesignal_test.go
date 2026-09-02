package pw

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// signalPayload is the shape a generated encoder produces: a value that appends
// itself as one JSON value.
type signalPayload struct{ Room string }

func (p signalPayload) AppendJSON(dst []byte) []byte {
	dst = append(dst, `{"room":`...)
	dst = append(dst, htmlbind.JSONString(p.Room)...)
	return append(dst, '}')
}

// liveScript yields what the script says: a string is a delivery, an error is
// whatever it is. It is the one source shape these tests need, because what is
// being checked is how the loop classifies each yield.
func liveScript(steps ...any) func(context.Context) iter.Seq2[string, error] {
	return func(ctx context.Context) iter.Seq2[string, error] {
		return func(yield func(string, error) bool) {
			for _, step := range steps {
				select {
				case <-ctx.Done():
					return
				default:
				}
				var (
					value string
					err   error
				)
				switch typed := step.(type) {
				case string:
					value = typed
				case error:
					err = typed
				}
				if !yield(value, err) {
					return
				}
			}
		}
	}
}

// livePageWithRecover is livePage with a recover subtree, which is the case that
// could not be made to work before v0.5.3: the module offered a delivery error
// to recovery first, so a signal rendered an error screen for something that
// succeeded.
func livePageWithRecover(source func(context.Context) iter.Seq2[string, error]) htmlbind.Fragment {
	outer := htmlbind.Builder[struct{}]{}
	primary := htmlbind.Builder[string]{}
	recovered := htmlbind.Builder[AsyncError]{}
	op := outer.Live(
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
		[]htmlbind.Op[AsyncError]{recovered.Static("<p>RECOVERED</p>")},
	)
	return (&htmlbind.Plan[struct{}]{
		HasAwaitBlock: true,
		HasLiveBlock:  true,
		Ops:           []htmlbind.Op[struct{}]{outer.Static("<main>"), op, outer.Static("</main>")},
	}).Bind(struct{}{})
}

// A signal is not a failure, so the loop that used to end on any error must keep
// ranging and must still write its terminal record.
//
// This is the regression the v0.5.3 migration names: a loop written as "any
// error ends this" returns on the first signal, the response closes with no end
// record, and the client reads that as truncation and reconnects. A working
// signal would look like a flaky connection rather than like an error.
func TestLiveSignalDoesNotEndTheStream(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveScript(
		"one",
		NewSignal("app.finish", signalPayload{Room: "3"}),
		"two",
	)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	lines := recordLines(t, recorder.Body.String())
	deliveries, signals := 0, 0
	for _, line := range lines {
		if strings.Contains(line, `"r":"await"`) {
			deliveries++
		}
		if strings.Contains(line, `"r":"signal"`) {
			signals++
		}
	}
	if deliveries != 2 {
		t.Errorf("deliveries = %d, want 2; a signal must not consume or end one", deliveries)
	}
	if signals != 1 {
		t.Errorf("signals = %d, want 1", signals)
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, `"r":"end"`) || !strings.Contains(last, `"reason":"done"`) {
		t.Errorf("last record = %q, want a done close; without one a client reads truncation", last)
	}
}

// The record is dispatched rather than applied, so it carries a name and a
// payload and none of the boundary bookkeeping a delivery carries.
func TestLiveSignalRecordCarriesNameAndPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveScript(
		NewSignal("app.finish", signalPayload{Room: "3"}),
	)))

	var record string
	for _, line := range recordLines(t, recorder.Body.String()) {
		if strings.Contains(line, `"r":"signal"`) {
			record = line
		}
	}
	if record == "" {
		t.Fatalf("no signal record in:\n%s", recorder.Body.String())
	}
	if !strings.Contains(record, `"name":"app.finish"`) {
		t.Errorf("record = %q, want the dispatch name", record)
	}
	if !strings.Contains(record, `"data":{"room":"3"}`) {
		t.Errorf("record = %q, want the encoded payload", record)
	}
	for _, absent := range []string{`"id":`, `"v":`, `"html":`} {
		if strings.Contains(record, absent) {
			t.Errorf("record = %q, carries %s; a signal addresses no region", record, absent)
		}
	}
}

// A signal reaching a clause that declared recover must not render it. This is
// the half that could not be fixed downstream at any price, and the half most
// likely to be silently wrong: the screen would show an error for a job that
// succeeded.
func TestLiveSignalDoesNotRenderTheRecoverSubtree(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePageWithRecover(liveScript(
		"one",
		NamedSignal("app.ping"),
		"two",
	)))

	body := recorder.Body.String()
	if strings.Contains(body, "RECOVERED") {
		t.Errorf("a signal rendered the recover subtree:\n%s", body)
	}
	lines := recordLines(t, body)
	if !strings.Contains(lines[len(lines)-1], `"reason":"done"`) {
		t.Errorf("last record = %q, want a done close", lines[len(lines)-1])
	}
	deliveries := 0
	for _, line := range lines {
		if strings.Contains(line, `"r":"await"`) {
			deliveries++
		}
	}
	if deliveries != 2 {
		t.Errorf("deliveries = %d, want 2; the subscription must survive a signal", deliveries)
	}
}

// The framework's own namespace carries the lifecycle names its client runtime
// dispatches, and a handler trusts one because application data has no route to
// it. A source reaching into it is dropped, and the stream carries on.
func TestLiveSignalUnderTheFrameworkPrefixIsRefused(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveScript(
		NamedSignal(ReservedSignalPrefix+"delivery_applied"),
		"one",
	)))

	body := recorder.Body.String()
	if strings.Contains(body, `"r":"signal"`) {
		t.Errorf("a reserved name reached the wire:\n%s", body)
	}
	lines := recordLines(t, body)
	if !strings.Contains(lines[len(lines)-1], `"reason":"done"`) {
		t.Errorf("last record = %q, want a done close; a refusal must not end the stream", lines[len(lines)-1])
	}
	if !strings.Contains(body, `"r":"await"`) {
		t.Errorf("the delivery after a refused signal was lost:\n%s", body)
	}
}

// The module refuses its own prefix at construction, and a faulted signal is a
// failure rather than a record, so it never reaches a client that would trust it.
func TestLiveSignalUnderTheModulePrefixNeverReachesTheWire(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveScript(
		NamedSignal("tb.delivery_applied"),
	)))

	if body := recorder.Body.String(); strings.Contains(body, `"r":"signal"`) {
		t.Errorf("a module-reserved name reached the wire:\n%s", body)
	}
}

// A response whose source only ever signals still commits and still closes. A
// screen driven entirely by signals renders nothing, so the commit path that
// runs on a first delivery has to run here too, or the stream would end with
// headers and no terminal record.
func TestLiveResponseOfOnlySignalsStillCloses(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveScript(
		NamedSignal("app.one"),
		NamedSignal("app.two"),
	)))

	if got := recorder.Header().Get("Content-Type"); got != liveMediaType {
		t.Errorf("Content-Type = %q, want %q", got, liveMediaType)
	}
	lines := recordLines(t, recorder.Body.String())
	if len(lines) < 2 || !strings.HasPrefix(lines[0], `{"r":"head"`) {
		t.Fatalf("records = %v, want the head record first", lines)
	}
	signals := 0
	for _, line := range lines {
		if strings.Contains(line, `"r":"signal"`) {
			signals++
		}
	}
	if signals != 2 {
		t.Errorf("signals = %d, want 2", signals)
	}
	if !strings.Contains(lines[len(lines)-1], `"reason":"done"`) {
		t.Errorf("last record = %q, want a done close", lines[len(lines)-1])
	}
}

// A real failure still ends the subscription, so the classification must not
// have widened into "any error keeps going".
func TestLiveFailureStillEndsTheStream(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, liveRequest("/"), livePage(liveScript(
		"one",
		errNotASignal,
		"two",
	)))

	body := recorder.Body.String()
	deliveries := 0
	for _, line := range recordLines(t, body) {
		if strings.Contains(line, `"r":"await"`) {
			deliveries++
		}
	}
	if deliveries != 1 {
		t.Errorf("deliveries = %d, want 1; a failure must still end the subscription", deliveries)
	}
}

// A payload is the one size on this wire an application chooses directly, on a
// connection that lives as long as a browser tab and with no render behind it to
// pace the writes. Reaching the budget closes for retry rather than dropping
// records: the reconnect re-executes the page and the source says the current
// thing again, where a dropped instruction is simply gone.
func TestLiveSignalsAreBoundedPerResponse(t *testing.T) {
	big := signalPayload{Room: strings.Repeat("x", 100)}
	// Sized so the first payload fits and the second does not, which is what
	// makes this a budget that accumulates rather than a per-record cap.
	config := pwconfig.DefaultHTMLConfig()
	config.LiveMaxSignalBytes = len(big.AppendJSON(nil)) + 1

	request := liveRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))

	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, livePage(liveScript(
		NewSignal("app.one", big),
		NewSignal("app.two", big),
		NewSignal("app.three", big),
	)))

	body := recorder.Body.String()
	if got := strings.Count(body, `"r":"signal"`); got != 1 {
		t.Errorf("signals = %d, want 1; the budget accumulates across a response:\n%s", got, body)
	}
	if !strings.Contains(body, `"reason":"retry"`) {
		t.Errorf("bound did not close for retry:\n%s", body)
	}
}

// A payload larger than the whole budget writes nothing rather than being let
// through once: the bound is a cap on bytes written, so the record that would
// exceed it is the one not written.
func TestLiveSignalOverTheWholeBoundWritesNothing(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	config.LiveMaxSignalBytes = 8

	request := liveRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))

	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, livePage(liveScript(
		NewSignal("app.one", signalPayload{Room: strings.Repeat("x", 100)}),
	)))

	body := recorder.Body.String()
	if strings.Contains(body, `"r":"signal"`) {
		t.Errorf("an over-budget payload reached the wire:\n%s", body)
	}
	if !strings.Contains(body, `"reason":"retry"`) {
		t.Errorf("bound did not close for retry:\n%s", body)
	}
}

// An unbounded configuration is still expressible, since a deployment whose
// sources are known quiet has nothing to bound.
func TestLiveSignalBoundIsOptional(t *testing.T) {
	config := pwconfig.DefaultHTMLConfig()
	config.LiveMaxSignalBytes = 0

	request := liveRequest("/")
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))

	big := signalPayload{Room: strings.Repeat("x", 100)}
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, livePage(liveScript(
		NewSignal("app.one", big),
		NewSignal("app.two", big),
	)))

	body := recorder.Body.String()
	if got := strings.Count(body, `"r":"signal"`); got != 2 {
		t.Errorf("signals = %d, want 2 with the bound off:\n%s", got, body)
	}
	if !strings.Contains(body, `"reason":"done"`) {
		t.Errorf("want a done close with the bound off:\n%s", body)
	}
}

var errNotASignal = &notASignalError{}

type notASignalError struct{}

func (e *notASignalError) Error() string { return "the source failed" }
