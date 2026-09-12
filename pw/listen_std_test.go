//go:build !tinygo

package pw

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// heldPort binds a wildcard port and keeps it for the length of the test. It is
// the same address the framework binds, which is what makes the collision the
// real one rather than an approximation of it: a loopback listener and a
// wildcard listener can coexist on one port on some platforms.
func heldPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port
	if port+developmentPortShift > 65535 {
		t.Skipf("the operating system handed out port %d, which leaves no room to shift into", port)
	}
	return port
}

func boundPort(t *testing.T, listener net.Listener) int {
	t.Helper()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener bound %s, which is not a TCP address", listener.Addr())
	}
	return address.Port
}

// Two projects open at once is an ordinary development day, and the second one
// used to get a bind failure and nothing served.
func TestADevelopmentRunMovesOffAPortItCannotBind(t *testing.T) {
	recorded := captureProcessLog(t)
	swapEnvForTest(t, EnvDevelopment, true)
	taken := heldPort(t)

	listener, err := listenApplication(ServerConfig{Port: taken})
	if err != nil {
		t.Fatalf("a development run refused to move off a bound port: %v", err)
	}
	defer listener.Close()

	port := boundPort(t, listener)
	if port <= taken || port > taken+developmentPortShift {
		t.Errorf("bound port %d, want one just past %d", port, taken)
	}
	// A process that is not on the port its configuration names has to say so,
	// or the developer reads the file and goes to the wrong address.
	output := recorded.String()
	if !strings.Contains(output, "level=WARN") {
		t.Errorf("the shift was not announced as a warning: %s", output)
	}
	for _, fragment := range []string{strconv.Itoa(taken), strconv.Itoa(port)} {
		if !strings.Contains(output, fragment) {
			t.Errorf("the warning does not mention %q: %s", fragment, output)
		}
	}
}

// An address is a contract everywhere else: the health check, the proxy, and the
// operator all go to the port the configuration names.
func TestEveryOtherEnvironmentBindsWhatItWasTold(t *testing.T) {
	swapEnvForTest(t, EnvStaging, true)
	taken := heldPort(t)

	listener, err := listenApplication(ServerConfig{Port: taken})
	if err == nil {
		port := boundPort(t, listener)
		_ = listener.Close()
		t.Fatalf("APP_ENV=%q answered on port %d instead of failing on %d", EnvStaging, port, taken)
	}
}

// The shift is a recovery, not a policy: a development run that can have the
// port it asked for takes it, and says nothing.
func TestAFreePortIsBoundAsConfiguredAndInSilence(t *testing.T) {
	recorded := captureProcessLog(t)
	swapEnvForTest(t, EnvDevelopment, true)
	// A port the operating system handed out and took back is the closest a test
	// can get to one nothing holds.
	probe, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	free := boundPort(t, probe)
	_ = probe.Close()

	listener, err := listenApplication(ServerConfig{Port: free})
	if err != nil {
		t.Fatalf("a free port was refused: %v", err)
	}
	defer listener.Close()

	if port := boundPort(t, listener); port != free {
		t.Errorf("bound port %d, want the configured %d", port, free)
	}
	if output := recorded.String(); output != "" {
		t.Errorf("binding the configured port still warned: %s", output)
	}
}
