package pwfast

import (
	"bufio"
	"bytes"
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// The two live loops are one protocol read twice, which is the thing pwruntime
// exists to prevent. A backend that ended its stream on the first signal would
// answer a different wire from the same page, so the classification is asserted
// here as well as on the net/http side rather than assumed to have been copied.

func liveSignalPage(steps ...any) HTMLFragment {
	outer := htmlbind.Builder[struct{}]{}
	primary := htmlbind.Builder[string]{}
	op := outer.Live(
		func(ctx context.Context, _ struct{}) []htmlbind.LiveBinding[string] {
			return []htmlbind.LiveBinding[string]{
				func(deliver func(func(*string), error) bool) error {
					for value, err := range liveSignalScript(ctx, steps...) {
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
		func(_ struct{}, err htmlbind.AsyncError) htmlbind.AsyncError { return err },
		[]htmlbind.Op[string]{
			primary.Static("<p>"),
			primary.Text(func(value string) string { return value }),
			primary.Static("</p>"),
		},
		[]htmlbind.Op[struct{}]{outer.Static("<p>waiting</p>")},
		nil,
	)
	return (&htmlbind.Plan[struct{}]{
		HasAwaitBlock: true,
		HasLiveBlock:  true,
		Ops:           []htmlbind.Op[struct{}]{outer.Static("<main>"), op, outer.Static("</main>")},
	}).Bind(struct{}{})
}

func liveSignalScript(ctx context.Context, steps ...any) iter.Seq2[string, error] {
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

func runLiveSignalStream(t *testing.T, steps ...any) string {
	t.Helper()
	// The loop takes the buffered writer this transport hands it, so the test
	// wraps one and flushes rather than passing a bare buffer.
	var body bytes.Buffer
	writer := bufio.NewWriter(&body)
	runLiveStream(writer, nil, liveSignalPage(steps...),
		pwruntime.UpdateSettings{}, nil, map[string]string{}, 0, nil, nil)
	_ = writer.Flush()
	return body.String()
}

// A signal is not a failure, so this loop must keep ranging and must still write
// its terminal record. Without the classification it returns on the first
// signal, the response closes with no end record, and the client reads that as
// truncation and reconnects.
func TestFastLiveSignalDoesNotEndTheStream(t *testing.T) {
	body := runLiveSignalStream(t, "one", htmlbind.NamedSignal("app.finish"), "two")

	deliveries, signals := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.Contains(line, `"r":"await"`) {
			deliveries++
		}
		if strings.Contains(line, `"r":"signal"`) {
			signals++
		}
	}
	if deliveries != 2 {
		t.Errorf("deliveries = %d, want 2; a signal must not consume or end one:\n%s", deliveries, body)
	}
	if signals != 1 {
		t.Errorf("signals = %d, want 1:\n%s", signals, body)
	}
	if !strings.Contains(body, `"reason":"done"`) {
		t.Errorf("no done close; a client reads a missing terminal record as truncation:\n%s", body)
	}
}

// The record is the same one the net/http half writes, because both call the
// same writer.
func TestFastLiveSignalRecordCarriesItsName(t *testing.T) {
	body := runLiveSignalStream(t, htmlbind.NamedSignal("app.finish"))

	if !strings.Contains(body, `{"r":"signal","name":"app.finish"}`) {
		t.Errorf("signal record missing or misshapen:\n%s", body)
	}
}

// This framework's namespace is reserved on both backends, or it would be
// reachable through whichever one did not guard it.
func TestFastLiveSignalUnderTheFrameworkPrefixIsRefused(t *testing.T) {
	body := runLiveSignalStream(t,
		htmlbind.NamedSignal(pwruntime.ReservedSignalPrefix+"delivery_applied"), "one")

	if strings.Contains(body, `"r":"signal"`) {
		t.Errorf("a reserved name reached the wire:\n%s", body)
	}
	if !strings.Contains(body, `"r":"await"`) {
		t.Errorf("the delivery after a refused signal was lost:\n%s", body)
	}
	if !strings.Contains(body, `"reason":"done"`) {
		t.Errorf("a refusal ended the stream:\n%s", body)
	}
}
