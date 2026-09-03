package pwconfig

import (
	"testing"

	"github.com/shibukawa/tinybind-go/configbind"
)

// A caller that overrides only the environment — the generated Cloudflare
// Workers entry does — must not lose the vendor name the configuration search
// needs, which is what an empty field used to mean.
func TestSetLoadOptionsKeepsTheDefaultsForEmptyFields(t *testing.T) {
	configState.Lock()
	previous, parsed := configState.options, configState.parsed
	configState.parsed = false
	configState.Unlock()
	defer func() {
		configState.Lock()
		configState.options, configState.parsed = previous, parsed
		configState.Unlock()
	}()

	SetLoadOptions(configbind.LoadOptions{Environ: []string{"APP_ENV=prod"}})
	configState.RLock()
	options := configState.options
	configState.RUnlock()
	if options.Vendor != defaultLoadOptions.Vendor || options.FileName != defaultLoadOptions.FileName {
		t.Errorf("defaults dropped: %+v", options)
	}
	if len(options.Environ) != 1 || options.Environ[0] != "APP_ENV=prod" {
		t.Errorf("environment not kept: %+v", options.Environ)
	}
}
