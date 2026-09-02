package pw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/contrib/otel/trace"
	"github.com/shibukawa/popcornweb/pwobservability"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

// The recorder itself lives in trace_test.go, beside the request root span it
// was written for. These are the readers a render assertion needs on top of it.
func (recorder *spanRecorder) named(name string) []trace.SpanData {
	var found []trace.SpanData
	for _, span := range recorder.ended() {
		if span.Name == name {
			found = append(found, span)
		}
	}
	return found
}

func (recorder *spanRecorder) one(t *testing.T, name string) trace.SpanData {
	t.Helper()
	found := recorder.named(name)
	if len(found) != 1 {
		t.Fatalf("want 1 %q span, got %d of %v", name, len(found), recorder.names())
	}
	return found[0]
}

func (recorder *spanRecorder) names() []string {
	spans := recorder.ended()
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name)
	}
	return names
}

func spanAttributes(span trace.SpanData) map[string]any {
	values := make(map[string]any, len(span.Attributes))
	for _, attribute := range span.Attributes {
		if value, ok := attribute.Value.AsString(); ok {
			values[attribute.Key] = value
			continue
		}
		if value, ok := attribute.Value.AsBool(); ok {
			values[attribute.Key] = value
			continue
		}
		if value, ok := attribute.Value.AsInt64(); ok {
			values[attribute.Key] = value
			continue
		}
		value, _ := attribute.Value.AsFloat64()
		values[attribute.Key] = value
	}
	return values
}

func renderTracing() *pwruntime.Tracing {
	return &pwruntime.Tracing{Render: true, Boundary: true, Database: true, Statement: true, MaxSQLLength: 4096}
}

// tracedRequest returns a browser request carrying config, the span policy, and
// the root span every render span is a child of.
func tracedRequest(t *testing.T, target string, config HTMLConfig, policy *pwruntime.Tracing) (*http.Request, *spanRecorder) {
	t.Helper()
	request := browserRequest(target)
	ctx := pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): config},
		Trace:   policy,
	})
	recorder := &spanRecorder{}
	ctx, root := trace.NewProvider(recorder).Tracer("test").Start(ctx, "request")
	t.Cleanup(root.End)
	return request.WithContext(ctx), recorder
}

func TestRenderSpanDescribesTheStreamedBranch(t *testing.T) {
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: true}, renderTracing())
	WriteHTML(httptest.NewRecorder(), request, asyncPage(asyncPageParams{Body: Resolved("ready")}))

	render := recorder.one(t, "render stream")
	attributes := spanAttributes(render)
	if got := attributes["pw.render.mode"]; got != renderModeStream {
		t.Errorf("pw.render.mode = %v, want %q", got, renderModeStream)
	}
	if got := attributes["pw.render.async"]; got != true {
		t.Errorf("pw.render.async = %v, want true", got)
	}
	if got := attributes["pw.render.live"]; got != false {
		t.Errorf("pw.render.live = %v, want false", got)
	}
	if got := attributes["pw.render.boundaries"]; got != int64(1) {
		t.Errorf("pw.render.boundaries = %v, want 1", got)
	}
	if got, _ := attributes["pw.render.bytes"].(int64); got <= 0 {
		t.Errorf("pw.render.bytes = %v, want the response size", attributes["pw.render.bytes"])
	}
}

// TestRenderSpanSeparatesTheInitialBuild is the shape the whole feature exists
// for: the shell and every fallback are one child span, and each boundary that
// replaces a fallback is another, so a waterfall shows what the visitor saw and
// when it changed.
func TestRenderSpanSeparatesTheInitialBuild(t *testing.T) {
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: true}, renderTracing())
	// The boundary settles after the initial pass, so the two spans cannot
	// collapse into one instant.
	body := Go(request.Context(), func(context.Context) (string, error) {
		time.Sleep(20 * time.Millisecond)
		return "ready", nil
	})
	WriteHTML(httptest.NewRecorder(), request, asyncPage(asyncPageParams{Body: body}))

	render := recorder.one(t, "render stream")
	initial := recorder.one(t, initialSpanName)
	boundary := recorder.one(t, boundarySpanName)

	if initial.ParentSpanID != render.SpanContext.SpanID() {
		t.Errorf("initial build parent = %q, want the render span", initial.ParentSpanID)
	}
	if boundary.ParentSpanID != render.SpanContext.SpanID() {
		t.Errorf("boundary parent = %q, want the render span", boundary.ParentSpanID)
	}
	// The initial build ends at the flush that commits the document, and the
	// boundary span starts at that same moment, so its extent is exactly how
	// long the fallback held the screen. The two timestamps are taken one after
	// the other rather than shared, so they coincide rather than match.
	if skew := boundary.StartTime.Sub(initial.EndTime); skew > time.Millisecond || skew < -time.Millisecond {
		t.Errorf("boundary starts %v from the commit, want them to coincide", skew)
	}
	if held := boundary.EndTime.Sub(boundary.StartTime); held < 10*time.Millisecond {
		t.Errorf("the fallback was reported as held for %v, want about the 20ms the work took", held)
	}
	if !initial.EndTime.Before(render.EndTime) {
		t.Error("the initial build did not end before the response did")
	}
	if got := spanAttributes(boundary)["pw.boundary.id"]; got != "tb-1" {
		t.Errorf("pw.boundary.id = %v, want tb-1", got)
	}
	if got, _ := spanAttributes(boundary)["pw.boundary.bytes"].(int64); got <= 0 {
		t.Error("the boundary span reported no fragment size")
	}
}

// TestBufferedRenderOpensNoInitialBuild keeps the tree honest: the whole render
// is the initial build there, and a child with its parent's extent would say
// the same thing twice.
func TestBufferedRenderOpensNoInitialBuild(t *testing.T) {
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: false}, renderTracing())
	WriteHTML(httptest.NewRecorder(), request, asyncPage(asyncPageParams{Body: Resolved("ready")}))

	render := recorder.one(t, "render buffered")
	if got := spanAttributes(render)["pw.render.mode"]; got != renderModeBuffered {
		t.Errorf("pw.render.mode = %v, want %q", got, renderModeBuffered)
	}
	if found := recorder.named(initialSpanName); len(found) != 0 {
		t.Errorf("buffered render opened %d initial build spans", len(found))
	}
	if found := recorder.named(boundarySpanName); len(found) != 0 {
		t.Errorf("buffered render opened %d boundary spans; nothing streams there", len(found))
	}
}

func TestFragmentRenderOpensItsOwnSpan(t *testing.T) {
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: true}, renderTracing())
	builder := htmlbind.Builder[struct{}]{}
	fragment := (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		builder.Static("<li>row</li>"),
	}}).Bind(struct{}{})

	WriteHTMLFragment(httptest.NewRecorder(), request, fragment)
	render := recorder.one(t, "render fragment")
	if got := spanAttributes(render)["pw.render.mode"]; got != renderModeFragment {
		t.Errorf("pw.render.mode = %v, want %q", got, renderModeFragment)
	}
	if got := spanAttributes(render)["pw.render.bytes"]; got != int64(len("<li>row</li>")) {
		t.Errorf("pw.render.bytes = %v", got)
	}
}

// TestLiveResponseSpansEachDelivery is the third rendering shape: a response
// that stays open, and whose spans are the stretches of time each region held
// its content.
func TestLiveResponseSpansEachDelivery(t *testing.T) {
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: true, Live: true}, renderTracing())
	request.Header.Set(ResponseModeHeader, LiveResponseMode)
	WriteHTML(httptest.NewRecorder(), request, livePage(liveValues("one", "two", "three")))

	render := recorder.one(t, "render live")
	attributes := spanAttributes(render)
	if got := attributes["pw.render.mode"]; got != renderModeLive {
		t.Errorf("pw.render.mode = %v, want %q", got, renderModeLive)
	}
	if got := attributes["pw.live.close_reason"]; got != liveCloseDone {
		t.Errorf("pw.live.close_reason = %v, want %q", got, liveCloseDone)
	}
	if got := attributes["pw.render.boundaries"]; got != int64(3) {
		t.Errorf("pw.render.boundaries = %v, want one per delivery", got)
	}
	// The document this response re-executed went to io.Discard, so the size is
	// the deliveries and nothing else.
	if got := attributes["pw.render.bytes"]; got != int64(len("<p>one</p><p>two</p><p>three</p>")) {
		t.Errorf("pw.render.bytes = %v, want the delivered fragments alone", got)
	}
	deliveries := recorder.named(deliverySpanName)
	if len(deliveries) != 3 {
		t.Fatalf("delivery spans = %d, want 3", len(deliveries))
	}
	for _, delivery := range deliveries {
		if delivery.ParentSpanID != render.SpanContext.SpanID() {
			t.Errorf("delivery parent = %q, want the live render span", delivery.ParentSpanID)
		}
		if got := spanAttributes(delivery)["pw.boundary.id"]; got != "tb-1" {
			t.Errorf("pw.boundary.id = %v, want tb-1", got)
		}
	}
	// Each delivery is measured from the one before it, so consecutive spans
	// abut rather than overlap: the waterfall reads as one region changing.
	if deliveries[1].StartTime.Before(deliveries[0].StartTime) {
		t.Error("delivery spans are not in delivery order")
	}
}

// TestRenderSpanReportsAFailedResponse covers the failure the request span
// cannot see: a stream that broke after commit still answers 200.
func TestRenderSpanReportsAFailedResponse(t *testing.T) {
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: false}, renderTracing())
	WriteHTML(httptest.NewRecorder(), request, asyncPage(asyncPageParams{}))

	render := recorder.one(t, "render buffered")
	if render.Status != trace.StatusError {
		t.Errorf("status = %v, want error for a render that never produced a document", render.Status)
	}
}

func TestRenderOpensNoSpanWhenTracingOff(t *testing.T) {
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: true}, nil)
	WriteHTML(httptest.NewRecorder(), request, asyncPage(asyncPageParams{Body: Resolved("ready")}))
	if names := recorder.names(); len(names) != 0 {
		t.Fatalf("framework spans with tracing off: %v", names)
	}
}

// TestBoundarySpansFollowTheirOwnSwitch covers the setting a busy page wants:
// the render span, without one span per boundary.
func TestBoundarySpansFollowTheirOwnSwitch(t *testing.T) {
	policy := renderTracing()
	policy.Boundary = false
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: true}, policy)
	WriteHTML(httptest.NewRecorder(), request, asyncPage(asyncPageParams{Body: Resolved("ready")}))

	render := recorder.one(t, "render stream")
	if found := recorder.named(boundarySpanName); len(found) != 0 {
		t.Errorf("boundary spans opened with the switch off: %d", len(found))
	}
	// The count survives, because it is an attribute rather than a span.
	if got := spanAttributes(render)["pw.render.boundaries"]; got != int64(1) {
		t.Errorf("pw.render.boundaries = %v, want 1", got)
	}
}

// TestRenderSpanParentsTheWorkInsideIt is what makes the tree readable: a
// statement a template runs belongs to the render, not to the request.
func TestRenderSpanParentsTheWorkInsideIt(t *testing.T) {
	request, recorder := tracedRequest(t, "/", HTMLConfig{Streaming: true}, renderTracing())
	builder := htmlbind.Builder[struct{}]{}
	page := (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		builder.RawCtx(func(ctx context.Context, _ struct{}) string {
			_, span := StartSpanContext(ctx, "load")
			span.End()
			return "<main>ok</main>"
		}),
	}}).Bind(struct{}{})

	WriteHTML(httptest.NewRecorder(), request, page)
	render := recorder.one(t, "render buffered")
	inner := recorder.one(t, "load")
	if inner.ParentSpanID != render.SpanContext.SpanID() {
		t.Errorf("inner span parent = %q, want the render span %q", inner.ParentSpanID, render.SpanContext.SpanID())
	}
}

// TestRenderSpanHangsOffTheRequestRoot is the claim the whole tree rests on:
// served through the real middleware chain, the render span is a child of the
// request span rather than a root of its own.
func TestRenderSpanHangsOffTheRequestRoot(t *testing.T) {
	recorder := tracedRuntime(t)
	resources := pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[HTMLConfig](): HTMLConfig{Streaming: true}},
		Trace:   renderTracing(),
	}
	handler, err := buildRuntimeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteHTML(w, r, asyncPage(asyncPageParams{Body: Resolved("ready")}))
	}), ServerConfig{}, SecurityConfig{}, MiddlewareConfig{}, resources, true)
	if err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), browserRequest("/orders"))

	request := recorder.one(t, "GET /orders")
	render := recorder.one(t, "render stream")
	if render.ParentSpanID != request.SpanContext.SpanID() {
		t.Errorf("render parent = %q, want the request span %q", render.ParentSpanID, request.SpanContext.SpanID())
	}
	if render.SpanContext.TraceID() != request.SpanContext.TraceID() {
		t.Error("the render span landed in a trace of its own")
	}
}

func TestResolveTracingFollowsTheExportSwitch(t *testing.T) {
	auto := ObservabilityConfig{Trace: defaultTraceConfig()}
	if resolveTracing(auto, false) != nil {
		t.Error("auto opened spans with nothing exporting them")
	}
	if resolveTracing(auto, true) == nil {
		t.Error("auto opened no span while traces were being exported")
	}

	off := ObservabilityConfig{Trace: defaultTraceConfig()}
	off.Trace.Enabled = QueryToggleOff
	if resolveTracing(off, true) != nil {
		t.Error("off opened spans anyway")
	}
	if traceForced(off) {
		t.Error("off forced the request root span")
	}

	on := ObservabilityConfig{Trace: defaultTraceConfig()}
	on.Trace.Enabled = QueryToggleOn
	if resolveTracing(on, false) == nil {
		t.Error("on opened no span without an exporter")
	}
	if !traceForced(on) {
		t.Error("on did not force the request root span, so its children would have no trace")
	}

	// Every switch below the parent off is the same as off, and resolves to no
	// policy rather than to a value that creates nothing.
	empty := ObservabilityConfig{Trace: TraceConfig{Enabled: QueryToggleOn}}
	if resolveTracing(empty, true) != nil {
		t.Error("a policy with nothing enabled was still installed")
	}
}

func TestResolveTracingTakesTheQueryTextBound(t *testing.T) {
	config := ObservabilityConfig{Trace: defaultTraceConfig()}
	config.Trace.Enabled = QueryToggleOn
	config.Query.MaxSQLLength = 64
	policy := resolveTracing(config, false)
	if policy == nil || policy.MaxSQLLength != 64 {
		t.Fatalf("MaxSQLLength = %v, want the query diagnostics bound", policy)
	}
	config.Query.MaxSQLLength = 0
	if policy := resolveTracing(config, false); policy.MaxSQLLength != pwobservability.DefaultMaxSQLLength {
		t.Errorf("MaxSQLLength = %d, want the default", policy.MaxSQLLength)
	}
}

func TestValidateTraceConfigRejectsAnUnknownToggle(t *testing.T) {
	err := validateTraceConfig(TraceConfig{Enabled: "yes"})
	if err == nil {
		t.Fatal("want an error for an unknown toggle")
	}
	if !strings.Contains(err.Error(), "observability.trace.enabled") {
		t.Errorf("error = %v, want it to name the key", err)
	}
}

// defaultTraceConfig is what the binding writes for an unconfigured project.
func defaultTraceConfig() TraceConfig {
	return TraceConfig{Enabled: QueryToggleAuto, Render: true, Boundary: true, Database: true, Statement: true}
}
