package session

import "testing"

// Duplicate type and key checks belong to the registry's state, not the call.
func TestRegistryRejectsDuplicateSlots(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register[payload]("other", Private, nil); err == nil {
		t.Errorf("a second slot for the same type was accepted")
	}
	if err := registry.Register[cart]("account", Private, nil); err == nil {
		t.Errorf("a second slot under a taken key was accepted")
	}
}

// A nil registry is a caller error rather than a panic, and a method on a nil
// pointer has to report it the way a function taking nil did.
func TestTheRegistryMethodRefusesANilRegistry(t *testing.T) {
	var registry *Registry
	if err := registry.Register[payload]("account", Private, nil); err == nil {
		t.Errorf("a nil registry accepted a slot")
	}
}
