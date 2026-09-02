package session

import (
	"reflect"
	"testing"
)

// Register is a method on the registry, so the slot it declares has to land in
// the registry the manager later freezes — including the duplicate checks,
// which are the registry's state rather than the call's.
func TestTheRegistryMethodDeclaresASlot(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register[payload]("account", Private, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	slot, ok := registry.lookup(reflect.TypeFor[payload]())
	if !ok {
		t.Fatalf("the slot the method declared is not in the registry")
	}
	if slot.key != "account" || slot.placement != Private {
		t.Errorf("slot = %q %v, want account Private", slot.key, slot.placement)
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
