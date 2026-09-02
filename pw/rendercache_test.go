package pw

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

type cachedPageParams struct{ Name string }

// cachedPage is a component declared with the cache annotation, as generation
// would emit it: an identity, a TTL, and an encoder for its one parameter.
//
// The counter is incremented by a value function that returns the parameter
// unchanged, so running the body is observable without changing what the body
// produces. A counter that reached the output would make every render a miss
// and the test would pass for the wrong reason.
func cachedPage(id string, runs *atomic.Int64, name string) HTMLFragment {
	builder := htmlbind.Builder[cachedPageParams]{}
	plan := &htmlbind.Plan[cachedPageParams]{
		Cache: &htmlbind.CachePolicy[cachedPageParams]{
			ID:  id,
			TTL: time.Minute,
			Key: func(params cachedPageParams) string { return htmlbind.KeyString(params.Name) },
		},
		Ops: []htmlbind.Op[cachedPageParams]{
			builder.Static("<main>"),
			builder.Text(func(params cachedPageParams) string {
				runs.Add(1)
				return params.Name
			}),
			builder.Static("</main>"),
		},
	}
	return plan.Bind(cachedPageParams{Name: name})
}

// resetRenderCache drops the process store, because it is keyed on the
// configuration and two tests configuring it identically would otherwise share
// entries in whatever order they happened to run.
func resetRenderCache(t *testing.T) {
	t.Helper()
	renderCacheState.Store(nil)
	t.Cleanup(func() { renderCacheState.Store(nil) })
}

func renderCached(t *testing.T, config HTMLConfig, page HTMLFragment) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))
	WriteHTML(recorder, request, page)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	return recorder.Body.String()
}

// TestRenderCacheReusesStoredOutput is the whole point of the wiring: the
// annotation was already compiled into every generated plan and did nothing,
// because no store was ever supplied.
func TestRenderCacheReusesStoredOutput(t *testing.T) {
	resetRenderCache(t)
	config := HTMLConfig{Cache: HTMLCacheConfig{Enabled: true, MaxEntries: 16}}
	var runs atomic.Int64

	first := renderCached(t, config, cachedPage("pw.test/reuse", &runs, "orders"))
	second := renderCached(t, config, cachedPage("pw.test/reuse", &runs, "orders"))

	if first != "<main>orders</main>" || second != first {
		t.Fatalf("bodies = %q and %q", first, second)
	}
	if got := runs.Load(); got != 1 {
		t.Errorf("the body ran %d times, want 1", got)
	}
}

// TestRenderCacheKeysOnParameters holds the key honest: the store is process
// wide, so a component whose parameters differ must not read another call's
// entry.
func TestRenderCacheKeysOnParameters(t *testing.T) {
	resetRenderCache(t)
	config := HTMLConfig{Cache: HTMLCacheConfig{Enabled: true, MaxEntries: 16}}
	var runs atomic.Int64

	first := renderCached(t, config, cachedPage("pw.test/keys", &runs, "orders"))
	second := renderCached(t, config, cachedPage("pw.test/keys", &runs, "invoices"))

	if first != "<main>orders</main>" || second != "<main>invoices</main>" {
		t.Fatalf("bodies = %q and %q", first, second)
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("the body ran %d times, want 2", got)
	}
}

// TestRenderCacheDisabledRendersEveryTime keeps the escape hatch real. It is
// also the behaviour every deployment had before this file existed, so it is
// what a rollback returns to.
func TestRenderCacheDisabledRendersEveryTime(t *testing.T) {
	resetRenderCache(t)
	config := HTMLConfig{Cache: HTMLCacheConfig{Enabled: false, MaxEntries: 16}}
	var runs atomic.Int64

	first := renderCached(t, config, cachedPage("pw.test/off", &runs, "orders"))
	second := renderCached(t, config, cachedPage("pw.test/off", &runs, "orders"))

	if first != "<main>orders</main>" || second != first {
		t.Fatalf("bodies = %q and %q", first, second)
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("the body ran %d times, want 2", got)
	}
}

// TestRenderCacheRebuildsOnReconfiguration covers the store being keyed on the
// configuration rather than built once: turning the cache off has to stop
// serving stored bytes, and turning it back on must not resurrect them.
func TestRenderCacheRebuildsOnReconfiguration(t *testing.T) {
	resetRenderCache(t)
	on := HTMLConfig{Cache: HTMLCacheConfig{Enabled: true, MaxEntries: 16}}
	off := HTMLConfig{Cache: HTMLCacheConfig{Enabled: false, MaxEntries: 16}}
	var runs atomic.Int64

	renderCached(t, on, cachedPage("pw.test/reconfig", &runs, "orders"))
	renderCached(t, off, cachedPage("pw.test/reconfig", &runs, "orders"))
	renderCached(t, on, cachedPage("pw.test/reconfig", &runs, "orders"))

	if got := runs.Load(); got != 3 {
		t.Errorf("the body ran %d times, want 3: each reconfiguration drops the store", got)
	}
}

// TestRenderCacheOptionAbsentWithoutStore keeps the no-store path exact. A
// generated plan reaching a nil store renders normally and computes no key,
// which is what makes a project using no annotation pay nothing.
func TestRenderCacheOptionAbsentWithoutStore(t *testing.T) {
	resetRenderCache(t)
	if option := renderCacheOption(t.Context(), HTMLCacheConfig{Enabled: false}); option != nil {
		t.Error("a disabled cache still handed the render an option")
	}
	if option := renderCacheOption(t.Context(), HTMLCacheConfig{Enabled: true, MaxEntries: 4}); option == nil {
		t.Error("an enabled cache handed the render no option")
	}
}

// TestRenderCacheCountsReportBothHalves covers the span attributes. A hit count
// alone cannot tell a working cache from one nothing is eligible for, which is
// the question an author with a guessed TTL actually has.
func TestRenderCacheCountsReportBothHalves(t *testing.T) {
	resetRenderCache(t)
	store := renderCache(HTMLCacheConfig{Enabled: true, MaxEntries: 4})
	if store == nil {
		t.Fatal("an enabled cache built no store")
	}
	counts := &renderCacheCounts{}
	counting := countingCache{store: store, counts: counts}

	if _, ok := counting.Get(t.Context(), "absent"); ok {
		t.Fatal("an empty store answered a key")
	}
	counting.Set(t.Context(), "absent", []byte("<p>stored</p>"), time.Minute)
	if _, ok := counting.Get(t.Context(), "absent"); !ok {
		t.Fatal("a stored key was not answered")
	}

	if hits, misses := counts.hits.Load(), counts.misses.Load(); hits != 1 || misses != 1 {
		t.Errorf("hits = %d, misses = %d, want 1 and 1", hits, misses)
	}
}
