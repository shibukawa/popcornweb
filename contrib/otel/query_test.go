package otel

import "testing"

// A trace backend outlives and outreaches the application database, so a
// password-reset token or an OAuth code that travelled in a query string must
// not be what it stores.
func TestQueryValuesDoNotReachATrace(t *testing.T) {
	for _, testCase := range []struct{ raw, want string }{
		{"token=8f3c9a", "token=REDACTED"},
		{"page=2&token=8f3c9a", "page=REDACTED&token=REDACTED"},
		{"code=abc&state=def", "code=REDACTED&state=REDACTED"},
		// A valueless parameter carries nothing, so it keeps its exact shape.
		{"debug", "debug"},
		{"debug&token=x", "debug&token=REDACTED"},
		// An empty value is still a value, and still says nothing.
		{"token=", "token=REDACTED"},
		// A value containing = or & keeps neither.
		{"sig=a=b", "sig=REDACTED"},
		{"", ""},
		{"&&", ""},
	} {
		if got := RedactedQuery(testCase.raw); got != testCase.want {
			t.Errorf("RedactedQuery(%q) = %q, want %q", testCase.raw, got, testCase.want)
		}
	}
}
