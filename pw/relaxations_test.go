package pw

import (
	"testing"
)

// Query bind values are logged automatically only in development.
func TestQueryDiagnosticsFollowTheEnvironment(t *testing.T) {
	if resolveQueryDiagnostics(queryConfig(nil), true) == nil {
		t.Error("auto should log queries in development")
	}
	if resolveQueryDiagnostics(queryConfig(nil), false) != nil {
		t.Error("auto should stay off outside development")
	}
}
