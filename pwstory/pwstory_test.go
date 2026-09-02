package pwstory

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

// A stand-in for what pw generates: a params struct, a plan, and a function
// binding one to the other.
type greetingParams struct {
	Title       string
	DisplayName string
	Count       int
	Tags        []string
}

var greetingOps = htmlbind.Builder[greetingParams]{}

var greetingPlan = &htmlbind.Plan[greetingParams]{
	Ops: []htmlbind.Op[greetingParams]{
		greetingOps.Static("<h1>"),
		greetingOps.Text(func(p greetingParams) string { return p.Title }),
		greetingOps.Static("</h1><p>"),
		greetingOps.Text(func(p greetingParams) string { return p.DisplayName }),
		greetingOps.Static("</p>"),
	},
}

func greeting(p greetingParams) htmlbind.Fragment { return greetingPlan.Bind(p) }

func register(t *testing.T) {
	t.Helper()
	registry.Lock()
	registry.templates = nil
	registry.document = nil
	registry.Unlock()
	Register(Template{
		Package: "templates", Name: "greeting", Exported: false,
		NewParams: func() any { return new(greetingParams) },
		Render:    func(p any) htmlbind.Fragment { return greeting(*p.(*greetingParams)) },
	})
}

func TestSynthesizedParametersAreRepresentativeAndNamed(t *testing.T) {
	params := &greetingParams{}
	Synthesize(params)
	if params.Title != "Title" {
		t.Errorf("Title = %q, want the field name read as words", params.Title)
	}
	if params.DisplayName != "Display Name" {
		t.Errorf("DisplayName = %q, want the field name split into words", params.DisplayName)
	}
	if params.Count == 0 {
		t.Error("a numeric field was left at zero, which shows nothing")
	}
	// One element would let a template that mishandles separators look right.
	if len(params.Tags) != sampleSlice {
		t.Errorf("Tags = %v, want %d elements", params.Tags, sampleSlice)
	}
}

// A story that changed every render would report nothing, so two renderings of
// the same template have to be identical.
func TestSynthesisIsDeterministic(t *testing.T) {
	first, second := &greetingParams{}, &greetingParams{}
	Synthesize(first)
	Synthesize(second)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("first = %+v, second = %+v", first, second)
	}
}

func TestStoryRendersTheTemplate(t *testing.T) {
	register(t)
	result := renderStory(Templates()[0], false)
	if result.Failed != "" {
		t.Fatalf("render failed: %s", result.Failed)
	}
	// Raw is what the template produced; Source is the same output indented for
	// reading, which is what the HTML tab shows.
	if !strings.Contains(result.Raw, "<h1>Title</h1>") {
		t.Errorf("raw = %q, want the synthesized title", result.Raw)
	}
	if !strings.Contains(result.Source, "<h1>\n  Title") {
		t.Errorf("source = %q, want it indented for reading", result.Source)
	}
}

// The list is sorted rather than left in initialisation order, which is not
// stable and would reshuffle the page between reloads.
func TestTemplatesAreOrdered(t *testing.T) {
	register(t)
	Register(Template{Package: "templates", Name: "alpha", NewParams: func() any { return new(greetingParams) },
		Render: func(p any) htmlbind.Fragment { return greeting(*p.(*greetingParams)) }})
	Register(Template{Package: "aaa", Name: "zeta", NewParams: func() any { return new(greetingParams) },
		Render: func(p any) htmlbind.Fragment { return greeting(*p.(*greetingParams)) }})
	names := []string{}
	for _, template := range Templates() {
		names = append(names, template.Package+"."+template.Name)
	}
	want := []string{"aaa.zeta", "templates.alpha", "templates.greeting"}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

func TestIndexAndStoryPagesAreServed(t *testing.T) {
	register(t)
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if body := recorder.Body.String(); !strings.Contains(body, "greeting") {
		t.Errorf("the index never listed the template:\n%s", body)
	}
	// An unexported template is exactly the one nothing else can show, so the
	// page says which it is rather than hiding the distinction.
	if body := recorder.Body.String(); !strings.Contains(body, "unexported") {
		t.Errorf("the index never marked the unexported template:\n%s", body)
	}

	recorder = httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/story/templates/greeting", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "&lt;h1&gt;") {
		t.Errorf("the story page never showed the emitted HTML:\n%s", body)
	}
}

// The preview is framed from a page carrying nothing of the storybook, so the
// harness stylesheet never reaches the markup under review.
func TestRawStoryCarriesOnlyTheTemplateOutput(t *testing.T) {
	register(t)
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/raw/templates/greeting", nil))
	body := recorder.Body.String()
	if strings.Contains(body, "storybook") {
		t.Errorf("the raw story carried the harness chrome:\n%s", body)
	}
	// A fragment has nowhere to link a stylesheet, so it is wrapped in a bare
	// document — one that adds a place for the project's own CSS and nothing
	// else, so what is seen is the template under the project's styles.
	if !strings.Contains(body, "<h1>Title</h1><p>Display Name</p>") {
		t.Errorf("raw = %q, want the template output", body)
	}
	for _, opinion := range []string{"<style", "class=", "background"} {
		if strings.Contains(body, opinion) {
			t.Errorf("the wrapper brought its own %q:\n%s", opinion, body)
		}
	}
}

// The stylesheet is linked where the fragment has no document of its own.
func TestStandaloneStoryLinksTheProjectStylesheet(t *testing.T) {
	t.Setenv(StylesVar, "/public/generated/app.css")
	if body := standaloneDocument("<h1>x</h1>"); !strings.Contains(body,
		`<link rel="stylesheet" href="/public/generated/app.css">`) {
		t.Errorf("body = %q, want the stylesheet linked", body)
	}
	// A project with no stylesheet links none, which is what its own pages do.
	t.Setenv(StylesVar, "")
	if body := standaloneDocument("<h1>x</h1>"); strings.Contains(body, "<link") {
		t.Errorf("body = %q, want no stylesheet", body)
	}
}

func TestUnknownStoryIsNotFound(t *testing.T) {
	register(t)
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/story/templates/nothing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// The console strips the mount before the request arrives, so every link the
// storybook writes has to carry it back.
func TestStorybookLinksCarryTheMount(t *testing.T) {
	register(t)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(panePrefixHeader, "/storybook")
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if !strings.Contains(body, `href="/storybook/story/templates/greeting"`) {
		t.Errorf("a story link did not carry the mount:\n%s", body)
	}
	if !strings.Contains(body, `href="/"`) {
		t.Errorf("the storybook offers no way back to the console:\n%s", body)
	}
}

// The HTML a template emits is written for a browser, on one line. What a
// developer opens the tab to check — which escaping context a value landed in —
// is unreadable that way.
func TestPrettyHTMLIndentsWithoutChangingContent(t *testing.T) {
	pretty := prettyHTML(`<div class="a"><p>one</p><br><p>two</p></div>`)
	for _, want := range []string{`<div class="a">`, "\n  <p>", "\n    one", "\n  <br>", "\n</div>"} {
		if !strings.Contains(pretty, want) {
			t.Errorf("pretty = %q, want it to contain %q", pretty, want)
		}
	}
	// A void element neither indents what follows nor expects a close.
	if strings.Contains(pretty, "</br>") {
		t.Error("a void element was given a closing tag")
	}
}

// Editing the parameters is what turns a story from an illustration into a
// question, so a supplied set has to reach the render.
func TestSuppliedParametersRenderInsteadOfTheSynthesizedOnes(t *testing.T) {
	register(t)
	result := renderStoryWith(Templates()[0], false, `{"Title":"typed","DisplayName":"by hand"}`)
	if result.Failed != "" {
		t.Fatalf("render failed: %s", result.Failed)
	}
	if !strings.Contains(result.Raw, "<h1>typed</h1>") {
		t.Errorf("raw = %q, want the supplied title", result.Raw)
	}
}

// A parameter set that will not parse keeps what was typed, so it can be
// corrected rather than retyped, and renders nothing from a guess.
func TestUnparsableParametersAreReportedAndKept(t *testing.T) {
	register(t)
	result := renderStoryWith(Templates()[0], false, `{"Title":`)
	if result.Failed == "" {
		t.Error("a broken parameter set was accepted")
	}
	if result.Params != `{"Title":` {
		t.Errorf("params = %q, want what was typed", result.Params)
	}
}
