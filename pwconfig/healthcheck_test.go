package pwconfig

import (
	"strings"
	"testing"
	"time"
)

func stashFrameworkAction(t *testing.T) {
	t.Helper()
	previous := selectedFrameworkAction()
	t.Cleanup(func() {
		frameworkActionState.Lock()
		frameworkActionState.action = previous
		frameworkActionState.Unlock()
	})
}

func TestParseFrameworkActionSelectsLeadingHealthcheck(t *testing.T) {
	stashFrameworkAction(t)

	args, err := ParseFrameworkAction([]string{"healthcheck", "--ready", "--timeout=1s"})
	if err != nil {
		t.Fatal(err)
	}
	// The returned slice must be empty but non-nil: a nil Args would send
	// configbind back to os.Args, where it would meet the token again.
	if args == nil || len(args) != 0 {
		t.Fatalf("args = %#v", args)
	}
	action := selectedFrameworkAction()
	if action.kind != frameworkActionHealthcheck {
		t.Fatalf("action = %#v", action)
	}
	if !action.healthcheck.ready || action.healthcheck.timeout != time.Second {
		t.Fatalf("healthcheck options = %#v", action.healthcheck)
	}
}

func TestParseFrameworkActionHealthcheckDefaults(t *testing.T) {
	stashFrameworkAction(t)

	if _, err := ParseFrameworkAction([]string{"healthcheck"}); err != nil {
		t.Fatal(err)
	}
	action := selectedFrameworkAction()
	if action.healthcheck.ready || action.healthcheck.timeout != defaultHealthcheckTimeout {
		t.Fatalf("healthcheck options = %#v", action.healthcheck)
	}
}

func TestParseFrameworkActionIgnoresNonLeadingHealthcheck(t *testing.T) {
	stashFrameworkAction(t)

	args, err := ParseFrameworkAction([]string{"--name", "healthcheck"})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "--name" || args[1] != "healthcheck" {
		t.Fatalf("args = %#v", args)
	}
	if action := selectedFrameworkAction(); action.kind != frameworkActionNone {
		t.Fatalf("action = %#v", action)
	}
}

func TestParseHealthcheckArgsRejectsUnknownOption(t *testing.T) {
	if _, err := parseHealthcheckArgs([]string{"--verbose"}); err == nil {
		t.Fatal("unknown healthcheck option was accepted")
	}
}

func TestParseHealthcheckArgsRejectsBadTimeout(t *testing.T) {
	for _, args := range [][]string{
		{"--timeout"},
		{"--timeout", "soon"},
		{"--timeout=-1s"},
		{"--timeout=0s"},
	} {
		if _, err := parseHealthcheckArgs(args); err == nil {
			t.Fatalf("timeout arguments %v were accepted", args)
		}
	}
}

func TestParseHealthcheckArgsSeparateTimeoutValue(t *testing.T) {
	action, err := parseHealthcheckArgs([]string{"--timeout", "250ms"})
	if err != nil {
		t.Fatal(err)
	}
	if action.healthcheck.timeout != 250*time.Millisecond {
		t.Fatalf("timeout = %v", action.healthcheck.timeout)
	}
}

func TestRunHealthcheckProbeRequiresConfiguredPath(t *testing.T) {
	err := runHealthcheckProbe(healthcheckOptions{timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "server.health") {
		t.Fatalf("err = %v", err)
	}
	err = runHealthcheckProbe(healthcheckOptions{ready: true, timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "server.readiness") {
		t.Fatalf("err = %v", err)
	}
}

func TestRefusePendingFrameworkActionNamesTheAction(t *testing.T) {
	stashFrameworkAction(t)

	frameworkActionState.Lock()
	frameworkActionState.action = frameworkAction{kind: frameworkActionHealthcheck}
	frameworkActionState.Unlock()
	if err := RefusePendingFrameworkAction(); err == nil || !strings.Contains(err.Error(), "healthcheck") {
		t.Fatalf("err = %v", err)
	}

	frameworkActionState.Lock()
	frameworkActionState.action = frameworkAction{}
	frameworkActionState.Unlock()
	if err := RefusePendingFrameworkAction(); err != nil {
		t.Fatal(err)
	}
}
