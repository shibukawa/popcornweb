package pwstory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

// AddressVar names the environment variable pw dev sets on the harness process
// to say where it should listen. The console proxies the pane to it, so the
// developer still sees one listener and one URL.
const AddressVar = "PW_STORYBOOK_ADDR"

// PublicVar names the project's public directory.
//
// A story rendered inside the document shell references whatever stylesheet the
// shell links, which for a Tailwind project is a file under the public tree. The
// harness is a different process on a different port, so that link resolved
// against the harness and found nothing — the story rendered unstyled and looked
// like a template bug rather than a missing mount.
const PublicVar = "PW_STORYBOOK_PUBLIC"

// StylesVar names the stylesheets a story should be rendered with, separated by
// spaces.
//
// A story rendered on its own is a fragment: there is no document around it and
// therefore nowhere for a stylesheet to be linked, so a Tailwind project saw
// every story unstyled and the toggle to the shell was the only way to see the
// real thing. Serving the public tree was necessary and not sufficient — the
// link had to exist too.
//
// pw resolves these, because it knows both the configured Tailwind output and
// the mount the public tree is served at, and the harness knows neither.
const StylesVar = "PW_STORYBOOK_STYLES"

// ListenAndServe runs the storybook. It is the whole body of the generated
// main, so that the generated file stays a list of imports and one call.
//
// The address is taken from the environment rather than a flag because the
// harness is started by pw dev and never by hand.
func ListenAndServe() error {
	address := strings.TrimSpace(os.Getenv(AddressVar))
	if address == "" {
		address = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	// The resolved address goes to stdout so pw can read it back when the
	// port was reserved rather than fixed.
	fmt.Printf("pw storybook listening on %s\n", listener.Addr())
	return (&http.Server{Handler: Handler()}).Serve(listener)
}

// Handler serves the storybook: an index of every registered template and one
// page per story.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", index)
	mux.HandleFunc("GET /story/{package}/{name}", story)
	mux.HandleFunc("POST /story/{package}/{name}", story)
	mux.HandleFunc("GET /raw/{package}/{name}", raw)
	// The public tree is served at the mount the document shell links, so a
	// story inside the shell finds the stylesheet the application would.
	if public := strings.TrimSpace(os.Getenv(PublicVar)); public != "" {
		mux.Handle("GET /public/", http.StripPrefix("/public/", http.FileServer(http.Dir(public))))
	}
	return mux
}

// rendering is one attempt to render a story, kept whole so a failure is shown
// in place of the story rather than in place of the page.
type rendering struct {
	// ParamsQuery is the parameter set encoded for the preview frame, so the
	// frame renders from what the page is showing rather than from the
	// synthesized set the page has moved on from.
	ParamsQuery string
	// Raw is exactly what the template produced; Source is the same output
	// indented for reading.
	Raw      string
	HTML     template.HTML
	Source   string
	Params   string
	Failed   string
	InShell  bool
	HasShell bool
}

func renderStory(t Template, shell bool) rendering { return renderStoryWith(t, shell, "") }

// renderStoryWith renders a story from supplied parameters, or from synthesized
// ones when none were supplied.
//
// Editing them is what turns a story from an illustration into a question a
// developer can ask: the synthesized set shows that the template renders, and a
// set the developer typed shows how it renders for the case they are worried
// about.
func renderStoryWith(t Template, shell bool, supplied string) rendering {
	result := rendering{InShell: shell, HasShell: len(document()) > 0}
	params := t.NewParams()
	Synthesize(params)
	if strings.TrimSpace(supplied) != "" {
		if err := json.Unmarshal([]byte(supplied), params); err != nil {
			// The typed value is kept so it can be corrected rather than
			// retyped, and the story is not rendered from something the
			// developer did not ask for.
			result.Params = supplied
			result.Failed = "parameters: " + err.Error()
			return result
		}
	}
	if encoded, err := json.MarshalIndent(params, "", "  "); err == nil {
		result.Params = string(encoded)
		result.ParamsQuery = string(encoded)
	}
	if strings.TrimSpace(supplied) != "" {
		result.Params, result.ParamsQuery = supplied, supplied
	}
	var out bytes.Buffer
	err := func() (err error) {
		// A template that panics is a defect the developer is looking for, so
		// it is reported as the story's result rather than taking the harness
		// down and with it every other story.
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("panic: %v", recovered)
			}
		}()
		fragment := t.Render(params)
		// A story has no session behind it, so there is no token to hand its
		// forms, and the render is told so. Told nothing, htmlbind refuses the
		// first unsafe form it reaches, which is the right answer for a
		// response and made every form component a failed story here. The
		// field still renders, empty, exactly as generation wrote it.
		if shell {
			return htmlbind.RenderChain(&out, document(), fragment, htmlbind.WithoutCSRFToken())
		}
		return htmlbind.Render(&out, fragment, htmlbind.WithoutCSRFToken())
	}()
	if err != nil {
		result.Failed = err.Error()
		return result
	}
	// Two forms of the same output: the exact bytes the template produced, which
	// is what the preview must render, and an indented copy, which is what a
	// person reads. Formatting the preview would change what is being shown.
	result.Raw = out.String()
	result.Source = prettyHTML(result.Raw)
	result.HTML = template.HTML(result.Raw)
	return result
}

func index(w http.ResponseWriter, r *http.Request) {
	writePage(w, r, indexPage, map[string]any{"Templates": Templates()})
}

func story(w http.ResponseWriter, r *http.Request) {
	t, ok := Lookup(r.PathValue("package"), r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	shell := r.URL.Query().Get("shell") == "1"
	supplied := ""
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		supplied = r.FormValue("params")
	}
	writePage(w, r, storyPage, map[string]any{
		"Template":  t,
		"Templates": Templates(),
		"Rendering": renderStoryWith(t, shell, supplied),
	})
}

// raw serves the story on its own, with nothing of the storybook around it, so
// the preview can be framed without the harness stylesheet reaching it.
func raw(w http.ResponseWriter, r *http.Request) {
	t, ok := Lookup(r.PathValue("package"), r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	shell := r.URL.Query().Get("shell") == "1"
	result := renderStoryWith(t, shell, r.URL.Query().Get("params"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if result.Failed != "" {
		http.Error(w, result.Failed, http.StatusInternalServerError)
		return
	}
	if shell {
		// The shell is a whole document and links what it links; adding to it
		// would be showing something other than what the application serves.
		_, _ = w.Write([]byte(result.Raw))
		return
	}
	_, _ = w.Write([]byte(standaloneDocument(result.Raw)))
}

// panePrefixHeader is how the console tells a pane where it is mounted. It
// matches devconsole.PanePrefixHeader, which is in an internal package this one
// cannot import.
const panePrefixHeader = "X-Pw-Pane-Prefix"

// writePage renders one storybook page.
//
// The mount prefix is added here rather than by each handler, because every
// link on every page needs it and a page that forgot would be the bug this
// exists to fix.
func writePage(w http.ResponseWriter, r *http.Request, page *template.Template, data map[string]any) {
	data["Prefix"] = r.Header.Get(panePrefixHeader)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := page.Execute(w, data); err != nil {
		fmt.Fprintf(w, "<p>storybook template error: %s</p>", template.HTMLEscapeString(err.Error()))
	}
}

// standaloneDocument wraps a fragment so a story on its own is still styled.
//
// A story rendered on its own is a fragment: there is no document around it and
// therefore nowhere for a stylesheet to be linked, so a Tailwind project saw
// every story unstyled and the toggle into the shell was the only way to see
// the real thing.
//
// The wrapper is a document and nothing more — no reset, no layout, no
// background of its own — so what is seen is the fragment under the project's
// own stylesheet rather than under this page's opinion of one.
func standaloneDocument(fragment string) string {
	var out strings.Builder
	out.WriteString(`<!doctype html><html><head><meta charset="utf-8">`)
	out.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	for _, href := range strings.Fields(os.Getenv(StylesVar)) {
		out.WriteString(`<link rel="stylesheet" href="` + template.HTMLEscapeString(href) + `">`)
	}
	out.WriteString(`</head><body>`)
	out.WriteString(fragment)
	out.WriteString(`</body></html>`)
	return out.String()
}
