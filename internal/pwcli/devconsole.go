package pwcli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/internal/dbseed"
	"github.com/shibukawa/popcornweb/internal/devconsole"
	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/tinybind-go/minitoml"
)

// startDevConsole serves the development console beside the application.
//
// A console that cannot listen is reported and skipped rather than fatal: the
// developer loop exists to run the application, and an unobservable run is
// still a working one. The port is fixed, so a collision is a real conflict
// with a real remedy, and saying which address is taken is more useful than
// quietly moving to another one.
func startDevConsole(root string, config projectConfig, telemetry *devTelemetryViewer, storybook *devStorybook, attach *devconsole.Attachment, stdout, stderr io.Writer) *devconsole.Console {
	if !config.Console.Enabled {
		return nil
	}
	server := readDevelopmentServer(root)
	console, err := devconsole.New(
		"127.0.0.1:"+strconv.Itoa(config.Console.Port),
		devconsole.Project{
			Name:           config.Name,
			Environment:    developmentEnvironment(),
			ApplicationURL: server.URL,
			APIDocURL:      server.APIDocURL(),
			APIDocKey:      "server.api_doc",
		},
		devConsolePanes(root, config, server, telemetry, storybook, attach),
		attach,
	)
	if err != nil {
		fmt.Fprintln(stderr, "pw dev: console:", err)
		return nil
	}
	fmt.Fprintf(stdout, "pw dev: console %s\n", console.URL())
	return console
}

// devConsolePanes lists every pane the console knows about, enabled or not. A
// disabled pane is listed with the key that would enable it rather than left
// out, so a developer who expected a surface is told why it is missing instead
// of wondering whether the version they run has it.
func devConsolePanes(root string, config projectConfig, server developmentServer, telemetry *devTelemetryViewer, storybook *devStorybook, attach *devconsole.Attachment) []devconsole.Pane {
	panes := []devconsole.Pane{{
		Slug:    "telemetry",
		Title:   "telemetry",
		Summary: "traces, logs, and the timing of what the application did",
		// The whole handler is mounted under the pane prefix, so this names no
		// path. The UI resolves its API against the served document and its
		// assets relatively, which is what lets receiver, snapshot API, and
		// page all follow the mount together.
		//
		// The endpoint the page displays follows too: it is the mount base,
		// and an exporter pointed there appends the OTLP paths, which this
		// mount serves. What pw injects into the application is still the
		// receiver's own listener, so both addresses work and neither lies.
		Handler:    telemetry.paneHandler(),
		DisabledBy: "dev.otel.enabled",
		// The viewer is a browser application with its own document, so
		// navigating to it left the developer with no way back to the console.
		// The frame puts the console navigation above a page the console does
		// not render.
		Framed: true,
	}}
	assets := devconsole.Pane{
		Slug:       "assets",
		Title:      "assets",
		Summary:    "what public/ serves now, and what a release build would serve",
		DisabledBy: "dev.console.assets.enabled",
	}
	if config.Console.Assets {
		assets.Handler = devconsole.AssetPane(devconsole.AssetSource{
			Root:            root,
			Public:          config.Assets.locations().public,
			Mount:           server.PublicMount,
			TailwindEnabled: config.Tailwind.Enabled,
			TailwindInput:   config.Tailwind.Input,
			TailwindOutput:  config.Tailwind.Output,
			// The build's own eligibility test, so the pane and pw build
			// cannot disagree about what would be compressed.
			Compressible: publicAssetCompressible,
		})
	}
	panes = append(panes, assets)
	// The storybook is listed whether or not it is running, because a pane the
	// developer expected and cannot find is worth an explanation.
	data := devconsole.Pane{
		Slug:       "data",
		Title:      "data",
		Summary:    "tables, rows, an editor, a statement console, and the project's declared queries",
		DisabledBy: "dev.console.data.enabled",
	}
	if config.Console.Data {
		// Served by the application itself, because the development database
		// is only addressable from inside the process that opened it.
		data.Handler = attach.Handler("the data pane")
		// Framed for the same reason the viewer is: the pane is a document of
		// its own, and only a frame keeps the console navigation above it.
		data.Framed = true
	}
	panes = append(panes, data, doctorPane(root))
	return append(panes, devconsole.Pane{
		Slug:       "storybook",
		Title:      "storybook",
		Summary:    "every generated template rendered on its own, with parameters made up from its type",
		Handler:    storybook.handler(),
		Framed:     true,
		DisabledBy: "dev.console.storybook.enabled",
	})
}

// The variables the application reads to find the console and to know whether
// a recovered build should reload the page. They match pw.DevConsoleURLVar and
// pw.DevConsoleReloadVar, which are declared in the pwdev half of the framework
// and so cannot be referenced from a host build.
const (
	envDevConsoleURL            = "PW_DEV_CONSOLE_URL"
	envDevConsoleReload         = "PW_DEV_CONSOLE_RELOAD"
	envDevAttachToken           = "PW_DEV_ATTACH_TOKEN"
	envDevConsoleOverlay        = "PW_DEV_CONSOLE_OVERLAY"
	envDevConsoleLauncher       = "PW_DEV_CONSOLE_LAUNCHER"
	envDevConsoleLauncherCorner = "PW_DEV_CONSOLE_LAUNCHER_CORNER"
)

// randomToken is the per-run secret the application presents when it announces
// the address of the pane it serves. It is generated rather than configured for
// the same reason requirement:contrib-devidp generates its client secret: a
// value written into a project is a value that outlives the run.
func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// consoleEnviron adds the resolved console address to the application
// environment, preserving a value the developer already exported.
//
// It is injected rather than configured for the same reason the OTLP endpoint
// is: pw dev resolves the address at startup, and a project that wrote it down
// would be committing a development port.
//
// Turning off everything that runs inside a page injects nothing to run. That
// is what makes the page the application serves byte-identical to a production
// render: with both the overlay and the launcher off, the framework serves no
// development module and the core carries no import of one, so there is nothing
// to turn off in the browser.
func consoleEnviron(console *devconsole.Console, settings consoleConfig, attachToken string, base []string) []string {
	if console == nil {
		return base
	}
	if value, ok := os.LookupEnv(envDevConsoleURL); ok && value != "" {
		return base
	}
	// The address is what the data pane announces to and what the overlay and
	// the launcher subscribe to, so it is injected whenever the console is
	// running. Their switches decide only what a page loads.
	base = append(base, envDevConsoleURL+"="+console.URL(), envDevAttachToken+"="+attachToken)
	if !settings.Overlay {
		base = append(base, envDevConsoleOverlay+"=0")
	}
	if !settings.Reload {
		base = append(base, envDevConsoleReload+"=0")
	}
	if !settings.Launcher {
		return append(base, envDevConsoleLauncher+"=0")
	}
	// The corner travels only with a launcher that is on, so a project that
	// turned it off does not hand the application a placement for something it
	// will not serve.
	return append(base, envDevConsoleLauncherCorner+"="+settings.LauncherCorner)
}

// developmentEnvironment is the APP_ENV the loop runs the application under,
// which is what startApplication injects.
func developmentEnvironment() string {
	if value, ok := os.LookupEnv(pwenv.Var); ok && value != "" {
		return value
	}
	return pwenv.Development
}

// developmentServer is what the console could learn about the running
// application by reading the project.
//
// Every field is best effort. This reads the development configuration file and
// is not the full resolution api:runtime-configuration performs: an environment
// variable or a flag outranks the file and is not consulted here. An
// undetermined value stays empty, and the console says undetermined rather than
// filling in a default that may be wrong.
type developmentServer struct {
	// URL is where the application listens, derived from server.port.
	URL string
	// PublicMount is server.public.mount, which the asset pane resolves each
	// file's URL through.
	PublicMount string
	// APIDoc is server.api_doc: scalar, swagger, or empty when the application
	// serves no documentation UI.
	APIDoc string
	// APIDocPath is server.api_doc_path. The default lives in the framework
	// rather than here, so an absent key means the framework's default applies
	// and the console can say which path that is.
	APIDocPath string
}

// defaultAPIDocPath mirrors the framework default for server.api_doc_path. It
// is duplicated rather than imported because the value belongs to the runtime
// configuration a host build does not link, and a wrong link here would send
// the developer to a 404 rather than fail loudly.
const defaultAPIDocPath = "/docs"

// APIDocURL is the absolute address of the documentation UI, or empty when the
// application serves none or the console could not place it.
func (s developmentServer) APIDocURL() string {
	if s.APIDoc == "" || s.URL == "" {
		return ""
	}
	path := s.APIDocPath
	if path == "" {
		path = defaultAPIDocPath
	}
	return s.URL + path
}

// readDevelopmentServer parses the development configuration once for every
// value the console wants out of it.
func readDevelopmentServer(root string) developmentServer {
	for _, name := range []string{
		pwenv.FileName(pwenv.Development),
		filepath.Join("config", pwenv.FileName(pwenv.Development)),
	} {
		source, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		document, err := minitoml.Parse(source)
		if err != nil {
			continue
		}
		server := developmentServer{}
		if value, ok := document.Get("server.port"); ok {
			if port, err := value.AsInt(); err == nil && port > 0 && port <= 65535 {
				server.URL = "http://localhost:" + strconv.FormatInt(port, 10)
			}
		}
		server.PublicMount = tomlString(document, "server.public.mount")
		server.APIDoc = tomlString(document, "server.api_doc")
		server.APIDocPath = tomlString(document, "server.api_doc_path")
		return server
	}
	return developmentServer{}
}

func tomlString(document minitoml.Document, key string) string {
	value, ok := document.Get(key)
	if !ok {
		return ""
	}
	text, err := value.AsString()
	if err != nil {
		return ""
	}
	return text
}

// storybookStyles is what a story rendered on its own should link.
//
// It is the Tailwind output resolved through the public mount, because that is
// the stylesheet the scaffolded document shell links and therefore the one a
// story is meant to be seen under. A project with Tailwind off links nothing,
// which is also what its own pages do.
func storybookStyles(config projectConfig, server developmentServer) []string {
	if !config.Tailwind.Enabled || config.Tailwind.Output == "" {
		return nil
	}
	mount := server.PublicMount
	if mount == "" {
		// The mount could not be read, so the scaffolded default is the best
		// available guess and a wrong stylesheet URL costs an unstyled story
		// rather than a wrong one.
		mount = "/public"
	}
	rest, ok := strings.CutPrefix(config.Tailwind.Output, config.Assets.locations().public+"/")
	if !ok {
		return nil
	}
	return []string{strings.TrimSuffix(mount, "/") + "/" + rest}
}

// hasSeedDatasets reports whether the project has anything to seed from. A
// project with no datasets is an ordinary shape, so the console offers no
// action rather than an action that would fail.
func hasSeedDatasets(root string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dbseed.DefaultDir)))
	return err == nil && info.IsDir()
}
