package handlers

// The work these routes share. None of it takes a request, so none of it is
// derived for the second build and both compile it — which is why it is not in
// the file the build tag excludes.
//
// It names pwruntime rather than pw for the same reason: a file both builds
// compile must not name one build's runtime, or the other links it.

import (
	"context"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
	httpbind "github.com/shibukawa/tinybind-go/httpbind"
)

// validationFields reports the field-level failures behind a pw.Parse error.
// The distinction matters: those are worth showing next to an input, and
// anything else is not.
func validationFields(err error) ([]pwruntime.FieldError, bool) {
	mapped, ok := httpbind.AsHTTPError(err)
	if !ok || len(mapped.Fields) == 0 {
		return nil, false
	}
	return mapped.Fields, true
}

func applyFieldErrors(form *FormState, fields []pwruntime.FieldError) {
	for _, field := range fields {
		switch field.Field {
		case "title":
			form.TitleError = field.Message
		case "owner":
			form.OwnerError = field.Message
		default:
			// priority and q are set by the page rather than typed, so a failure
			// there means the request did not come from this form.
			form.FormError = field.Field + " " + field.Message
		}
	}
}

// knownPriority keeps the select on a value it actually offers. Anything else
// did not come from this form, and the rejection is already reported above it.
func knownPriority(value string) string {
	switch value {
	case "low", "normal", "high":
		return value
	default:
		return "normal"
	}
}

func emptyLabel(query string, matched int) string {
	switch {
	case matched > 0:
		return ""
	case query != "":
		return "Nothing matches “" + query + "”."
	default:
		return "No tasks yet."
	}
}

func summarize(ctx context.Context) (Summary, error) {
	if err := sleep(ctx, 600*time.Millisecond); err != nil {
		return Summary{}, err
	}
	total, high := tasks.counts()
	return Summary{Total: total, High: high, Took: "600ms"}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
