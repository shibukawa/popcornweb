package testutil

import (
	"testing"

	"github.com/shibukawa/popcornweb/internal/pwtestbridge"
)

// The three configuration operations are methods on the isolated copy. Each
// assertion writes through one and reads through another, so an operation
// reaching the wrong stored slot is found here rather than in a test that only
// reads back what it wrote.
func TestTheConfigMethodsReachTheSameValues(t *testing.T) {
	config := &Config{values: pwtestbridge.Configs{}}

	config.Set(fixtureConfig{Name: "set", Labels: []string{"a"}})
	if got := config.Get[fixtureConfig]().Name; got != "set" {
		t.Errorf("Get = %q, want set; Set wrote somewhere Get does not read", got)
	}

	config.Update(func(value *fixtureConfig) { value.Name = "updated" })
	if got := config.Get[fixtureConfig]().Name; got != "updated" {
		t.Errorf("Get = %q, want updated", got)
	}
	if got := config.Get[fixtureConfig]().Labels; len(got) != 1 || got[0] != "a" {
		t.Errorf("Labels = %v, want [a]; Update replaced the value rather than editing it", got)
	}
}

// Every operation copies, which is what keeps one test's edit out of the next
// test's configuration. The methods have to inherit that rather than hand out
// the stored value.
func TestTheConfigMethodsHandBackACopy(t *testing.T) {
	config := &Config{values: pwtestbridge.Configs{}}
	config.Set(fixtureConfig{Name: "stored", Labels: []string{"a"}})

	got := config.Get[fixtureConfig]()
	got.Labels[0] = "edited"

	if stored := config.Get[fixtureConfig]().Labels[0]; stored != "a" {
		t.Errorf("the stored value became %q; Get handed back the slice it holds", stored)
	}
}

// A nil configuration is what a caller has before TestRun builds one. Get
// answers a zero value rather than panicking, on the same nil pointer.
func TestTheConfigGetMethodAnswersOnANilConfig(t *testing.T) {
	var config *Config
	if got := config.Get[fixtureConfig]().Name; got != "" {
		t.Errorf("Get = %q, want the zero value", got)
	}
}
