package pwcli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/popcornweb/internal/pwgen"
	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/sessionstore"

	// The CLI writes a migration in any dialect, so it links every engine the
	// framework knows rather than the one this machine happens to run.
	_ "github.com/shibukawa/popcornweb/authstate/mysql"
	_ "github.com/shibukawa/popcornweb/authstate/postgres"
	_ "github.com/shibukawa/popcornweb/authstate/sqlite"
	_ "github.com/shibukawa/popcornweb/sessionstore/mysql"
	_ "github.com/shibukawa/popcornweb/sessionstore/postgres"
	_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"
)

// Where the starter page sends a reader who wants more than it holds. Literal,
// because a landing page that needs a lookup to render is not a starting point.
const (
	documentationURL = "https://shibukawa.github.io/popcornweb/"
	repositoryURL    = "https://github.com/shibukawa/popcornweb"
)

const initUsage = "usage: pw init [<project-name>] [--yes] [--router=registered|discovered|both] [--tailwind] [--tinygo] [--no-devbox] [--no-database] [--db=sqlite|postgres|mysql] [--dynamo] [--no-redis] [--auth=none|oidc|oidc-passkey|passkey] [--session=dev-volatile|dev-persist|rdb|cookie|redis|dynamo] [--devidp] [--skills=claude|agents|none]"

// Authentication modes the wizard and the --auth flag select between. They map
// onto the plugin/auth modes, with none meaning no [auth] configuration.
const (
	authNone        = "none"
	authOIDC        = "oidc"
	authOIDCPasskey = "oidc-passkey"
	authPasskey     = "passkey"
	// authJWTOnly verifies a bearer token somebody else issued. It is absent
	// from the wizard question and from --auth on purpose: it is reached by
	// naming the project shape it belongs to, which is the api-server preset,
	// rather than by answering a question every browser application also
	// answers.
	authJWTOnly = "jwt-only"
)

// authDevelopmentIssuer is the issuer name a scaffolded bearer project runs
// with until an operator supplies a real one.
//
// Nothing is served there and nothing contacts it: under pw dev the relaxed
// path never calls the verifier, so no metadata and no key set is fetched. It
// exists because the mode refuses to start without an issuer — which is the
// right rule everywhere — and because the identity a request resolves to is
// derived from the issuer and the subject together, so a token has to name one.
//
// It is a loopback address on a port nothing runs on, which is the shape least
// likely to be mistaken for a provider somebody should be pointing at.
const authDevelopmentIssuer = "http://127.0.0.1:9999/dev-issuer"

// servesBrowserLogin reports whether the mode mounts a login ceremony a person
// walks through. The bearer mode authenticates every request from a header and
// creates no account, so everything a login implies — a session record, an
// identity provider, a CSRF token, the framework-owned ceremony tables — is
// about the other modes.
func servesBrowserLogin(options initOptions) bool {
	return servesLogin(options) && options.Auth != authJWTOnly
}

// Session storage backends and development intent modes selected by the wizard
// and --session. General server backends are contributed by blank imports;
// cookie and both development modes are built into pw.
const (
	sessionRDB         = "rdb"
	sessionCookie      = "cookie"
	sessionDevVolatile = "dev-volatile"
	sessionDevPersist  = "dev-persist"
	sessionRedis       = "redis"
	sessionDynamo      = "dynamo"
	// sessionFirestore keeps records in Firestore in Datastore mode, for the
	// same relational-free project on Google Cloud that dynamo serves on AWS.
	sessionFirestore = "firestore"
)

// usesOIDC reports whether a mode needs an OpenID Provider.
func usesOIDC(mode string) bool { return mode == authOIDC || mode == authOIDCPasskey }

// usesPasskey reports whether the mode mounts the ceremony endpoints, which
// decides whether the project needs a relying-party registration and the
// browser side that calls navigator.credentials.
func usesPasskey(mode string) bool { return mode == authOIDCPasskey || mode == authPasskey }

// sessionBackend is the selected backend.
//
// An unanswered project takes cookie when it serves no login and rdb when it
// does. Session storage is not a login, so a project that only remembers a
// language preference still gets the middleware; what it does not need is a
// table, a migration, and a storage import to hold state that fits in a sealed
// cookie. A project with a login writes a record on every sign-in and normally
// wants to end one on demand, which is what rdb is for.
func sessionBackend(options initOptions) string {
	if options.Session != "" {
		return options.Session
	}
	if servesBrowserLogin(options) {
		// The default follows the store the project actually has. A relational
		// default here would name a pool nothing opens.
		if !options.Database && options.Dynamo {
			return sessionDynamo
		}
		if !options.Database && options.Firestore {
			return sessionFirestore
		}
		return sessionRDB
	}
	return sessionCookie
}

// developmentSessionBackend selects the backend written to config.dev.toml.
// An explicit storage answer is respected; an unanswered scaffold starts with
// process-local state so restarts discard records written by an older codec.
func developmentSessionBackend(options initOptions) string {
	if options.SessionExplicit {
		return sessionBackend(options)
	}
	return sessionDevVolatile
}

// generatedKeyringSecret returns a fresh session keyring for the scaffolded
// development configuration.
//
// It is written into config.dev.toml as a literal rather than left for the
// developer to author, because requiring an authored secret to run a scaffolded
// project puts a deployment concern in the way of getting started. It is
// written to a file rather than generated at startup because a keyring
// generated per process dies with the process: restarting the developer loop
// would sign every developer out and empty every cart and preference being
// worked on. The devidp client credentials are generated per run for the
// opposite reason, that they mean nothing beyond one run.
//
// It is per project rather than a constant in this source, so it is not a
// published credential and the placeholder check has nothing to match. It still
// belongs to development only: every other environment reads
// SESSION_KEYRING_SECRET, and pw doctor reports a literal here as an error for
// any environment but dev.
func generatedKeyringSecret() string {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// crypto/rand does not fail in practice, and a scaffold that quietly
		// wrote a predictable secret would be worse than one that stops.
		panic("pw init: cannot generate a session keyring: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(secret)
}

// sessionBackendPlugin names the import that registers a backend. The cookie
// backend is built into pw, so it needs none.
func sessionBackendPlugin(backend, engine string) string {
	switch backend {
	case sessionRDB:
		// A SQL store is one package per engine, because no engine reads
		// another's DDL.
		return "github.com/shibukawa/popcornweb/sessionstore/" + engineDialect(engine)
	case sessionRedis:
		return "github.com/shibukawa/popcornweb/sessionstore/redis"
	case sessionDynamo:
		return "github.com/shibukawa/popcornweb/sessionstore/dynamo"
	case sessionFirestore:
		return "github.com/shibukawa/popcornweb/sessionstore/firestore"
	default:
		return ""
	}
}

// Router answers of pw init. The two routers coexist on one mux, so this
// decides which of them the scaffold writes rather than how the framework
// behaves.
//
// These three names are the project's vocabulary for the pair, and everything
// that talks about a router uses them: the flag, the wizard, and the capability
// catalog of pw add. What each router reads is a directory, and which directory
// is a data:project-config value rather than part of the name.
const (
	// routerRegistered writes registrations in Go: any method, any response,
	// and the generated OpenAPI document.
	routerRegistered = "registered"
	// routerDiscovered derives routes from a directory tree: a directory
	// holding a page template is a route, and generation writes the
	// registration.
	routerDiscovered = "discovered"
	routerBoth       = "both"
)

// The directories pw init scaffolds each router into. They are defaults rather
// than fixed names: generation, pw new, and pw dev all read the data:project-config
// purpose lists, so a project renames a tree by editing popcornweb.toml and
// moving the directory.
const (
	defaultRegisteredDir = "handlers"
	defaultDiscoveredDir = "pages"
	defaultTemplatesDir  = "templates"
	// defaultFirestoreDir holds the firestore-tagged types and .pw.firestore
	// queries of the Firestore store. It is separate from the DynamoDB
	// directory because each is its own generate purpose, and a directory
	// listed for one is not a generation source for the other.
	defaultFirestoreDir = "entities"
	// defaultDynamoDir holds the dynamo-tagged types and .pw.dynamo queries of
	// requirement:dynamodb-store. It is its own purpose because a directory
	// contributes only the artifact kinds whose purpose lists it.
	defaultDynamoDir = "records"
)

// dynamoDevboxPackage is the local DynamoDB server pw dev starts, the address of
// which is what the scaffolded endpoint points at.
const dynamoDevboxPackage = "dynamodb-local@latest"

// dynamoStore names DynamoDB where a store is being chosen rather than an
// engine. It is not an engine name and never reaches project.database, because
// requirement:dynamodb-store is a second kind of store rather than a fourth
// dialect.
const dynamoStore = "dynamo"

// firestoreStore names Firestore where a store is being chosen rather than an
// engine, on the same terms as dynamoStore.
const firestoreStore = "firestore"

// effectiveRouter reads an unset answer as the registered router: that is the
// shape every project scaffolded before page trees existed has, and the one a
// caller that never asked should get.
func effectiveRouter(router string) string {
	if router == "" {
		return routerRegistered
	}
	return router
}

func routerHasRegistered(router string) bool {
	router = effectiveRouter(router)
	return router == routerRegistered || router == routerBoth
}

func routerHasDiscovered(router string) bool {
	return router == routerDiscovered || router == routerBoth
}

func validRouter(router string) bool {
	return router == routerRegistered || router == routerDiscovered || router == routerBoth
}

// initOptions holds every project bootstrap choice. Shortcut flags and the
// wizard produce the same value, and scaffoldFiles is its only consumer.
type initOptions struct {
	Name string
	// Kind is the project.kind this scaffold writes. Empty means an
	// application, which is what every answer below describes; kindPackage
	// produces a module with no binary of its own, and the questions that
	// describe an application do not apply to it.
	Kind string
	// Preset records which requirement:init-presets entry supplied the
	// answers, so the review screen can say which one it is showing. It is a
	// label rather than a scaffold input: nothing downstream reads it, and a
	// created project records no preset it came from.
	Preset string
	// Router selects the routers this project starts with. Either can be
	// installed later, so this is a starting point rather than a mode.
	Router   string
	TinyGo   bool
	Tailwind bool
	// FastHTTP declares the fasthttp backend as a second build target. It adds
	// a build rather than replacing one, so a project taking it still writes
	// net/http handlers and still runs on net/http; what it buys is that
	// generation constrains its net/http output and derives the second build's
	// handlers and page tree from it.
	FastHTTP bool
	// Images installs the build-time image conversion and the encoders it
	// runs. It is separate from the other asset answers because it is the only
	// one whose usefulness depends on host tools.
	Images bool
	// Database scaffolds the rdb configuration, the migration directory, and
	// the SQL example. Declining it removes all three together, because none
	// of them is useful without the others.
	Database bool
	// Engine names the database. It decides the DSN, the dialect of the
	// starter schema, the development server, and the driver the binary links,
	// and applies only to a project that took the database.
	Engine string
	// Redis adds the Valkey development server to the Devbox environment.
	Redis bool
	// Devbox scaffolds the reproducible development environment. Declining it
	// leaves the toolchain and the services to the operator, which is what a
	// project already standardized on Nix, Docker, or asdf wants.
	Devbox bool
	Auth   string
	// Session selects where login sessions are stored. It only applies to a
	// project that scaffolds a login.
	Session string
	// SessionExplicit distinguishes an operator's storage answer from the
	// deployment default already present in Session.
	SessionExplicit bool
	// AuthEmulator scaffolds the development identity provider instead of
	// pointing the project at an external one. It only applies to an OIDC mode.
	AuthEmulator bool
	// AuthStore is the store the login was chosen through: an engine name, or
	// dynamoStore. It is a wizard answer rather than a scaffold input — what it
	// writes is already in Database, Engine, and Dynamo — and it exists so the
	// follow-up question knows which store the operator has not been asked
	// about yet.
	AuthStore string
	// Dynamo adds the DynamoDB store. It is a second kind of store rather than
	// a fourth SQL engine, so it stands beside the Database answer instead of
	// replacing it, and either, both, or neither is a valid project.
	Dynamo bool
	// Firestore adds the Firestore store, in Datastore mode. It stands beside
	// the Database answer on the same terms as Dynamo, and the two are
	// independent: a project may have either, both, or neither.
	Firestore bool
	// Skills selects the agent directory the bundled framework skill is placed
	// in, or none. It answers a question about the machines this project is
	// edited on rather than about the project shape, which is why a preset
	// leaves it alone the way it leaves the name alone.
	Skills string
	// Yes skips the wizard and takes the flags with the defaults for everything
	// they do not answer. It is the only way to run non-interactively in a
	// terminal, because the project name alone no longer means the caller has
	// answered anything but the name.
	Yes bool
}

// defaultInitOptions answers for the project most people are starting. Every
// answer here is one pw add can revisit, except the toolchain, which is why
// that one defaults to the reversible direction.
func defaultInitOptions() initOptions {
	return initOptions{
		// Router is left unset rather than defaulted: effectiveRouter reads that
		// as the registered tree, which keeps the rule in one place and a
		// scripted run on the shape it has always produced.
		//
		// TinyGo is left unset for a related reason and a stronger one. The
		// routing is compatible either way — decision:stdlib-servemux is what a
		// host project registers on too — so the answer buys a smaller binary
		// and a wasm target rather than a different application. What it costs
		// is a toolchain in devbox.json that a project not building for it never
		// runs, and the one answer pw add cannot revisit later. A default nobody
		// can undo has to be the one that assumes less.
		Devbox:   true,
		Database: true,
		Engine:   engineSQLite,
		Redis:    true,
		Auth:     authNone,
		Session:  sessionRDB,
		// The skill costs a directory of markdown and nothing at runtime, and
		// a project edited by no agent deletes it the way it deletes .vscode/.
		Skills: skillsClaude,
	}
}

func parseInitArgs(args []string) (initOptions, error) {
	options := defaultInitOptions()
	// A preset is read before anything else, because it decides whether the
	// rest of the flags are answers or a conflict.
	preset, presetGiven, err := parsePresetArgs(args)
	if err != nil {
		return initOptions{}, err
	}
	var positional []string
	// Tracked so --db can contradict --no-database. Without it the engine
	// would silently apply to a project that has no database to apply it to.
	engineSelected := false
	// Tracked for the same reason: the default session backend is relational,
	// and a project with no relational database has to move off it rather than
	// scaffold a pool nothing opens.
	sessionSelected := false
	for _, arg := range args {
		switch arg {
		case "--tailwind":
			options.Tailwind = true
		case "--no-tailwind":
			options.Tailwind = false
		case "--tinygo":
			options.TinyGo = true
		case "--no-tinygo":
			// The answer this used to change is now the default, so it selects
			// nothing. It stays accepted, and out of the usage line, because a
			// script that passes it means what it says and refusing it would
			// break that script over a word it got right.
			options.TinyGo = false
		case "-y", "--yes":
			options.Yes = true
		case "-i", "--interactive":
			// Accepted and ignored: the wizard it used to request is now what
			// every terminal run does.
		case "--devbox":
			options.Devbox = true
		case "--no-devbox":
			options.Devbox = false
		case "--database":
			options.Database = true
		case "--no-database":
			options.Database = false
		case "--dynamo":
			options.Dynamo = true
		case "--no-dynamo":
			options.Dynamo = false
		case "--firestore":
			options.Firestore = true
		case "--no-firestore":
			options.Firestore = false
		case "--redis":
			options.Redis = true
		case "--no-redis":
			options.Redis = false
		case "--devidp":
			options.AuthEmulator = true
		case "--no-devidp":
			options.AuthEmulator = false
		default:
			// Both were read by parsePresetArgs above, which also settled what
			// they mean together.
			if strings.HasPrefix(arg, "--preset=") || strings.HasPrefix(arg, "--kind=") {
				continue
			}
			if backend, ok := strings.CutPrefix(arg, "--session="); ok {
				switch backend {
				case sessionDevVolatile, sessionDevPersist, sessionRDB, sessionCookie, sessionRedis, sessionDynamo, sessionFirestore:
					options.Session = backend
					options.SessionExplicit = true
					sessionSelected = true
				default:
					return initOptions{}, fmt.Errorf("init: --session must be %s, %s, %s, %s, %s, %s, or %s",
						sessionDevVolatile, sessionDevPersist, sessionRDB, sessionCookie, sessionRedis, sessionDynamo, sessionFirestore)
				}
				continue
			}
			if engine, ok := strings.CutPrefix(arg, "--db="); ok {
				if !validEngine(engine) {
					return initOptions{}, fmt.Errorf("init: --db must be %s", engineNames())
				}
				options.Engine = engine
				engineSelected = true
				continue
			}
			if router, ok := strings.CutPrefix(arg, "--router="); ok {
				if !validRouter(router) {
					return initOptions{}, fmt.Errorf("init: --router must be %s, %s, or %s",
						routerRegistered, routerDiscovered, routerBoth)
				}
				options.Router = router
				continue
			}
			if value, ok := strings.CutPrefix(arg, "--skills="); ok {
				if !validSkills(value) {
					return initOptions{}, fmt.Errorf("init: --skills must be %s, %s, or %s",
						skillsClaude, skillsAgents, skillsNone)
				}
				options.Skills = value
				continue
			}
			if mode, ok := strings.CutPrefix(arg, "--auth="); ok {
				switch mode {
				case authNone, authOIDC, authOIDCPasskey, authPasskey:
					options.Auth = mode
				default:
					return initOptions{}, fmt.Errorf("init: --auth must be %s, %s, %s, or %s",
						authNone, authOIDC, authOIDCPasskey, authPasskey)
				}
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return initOptions{}, fmt.Errorf("init: unknown option %q", arg)
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) > 1 {
		return initOptions{}, errors.New(initUsage)
	}
	if len(positional) == 1 {
		options.Name = strings.TrimSpace(positional[0])
	}
	if presetGiven {
		// The preset is the answer to every question the flags above answer,
		// and presetConflict has already refused a run that gave both. What is
		// left to carry over is the name and --yes, which answer neither.
		return applyPresetArgs(preset, options)
	}
	if !usesOIDC(options.Auth) {
		options.AuthEmulator = false
	}
	if !options.Devbox && options.Redis {
		// The Valkey answer only ever writes a Devbox package, so without the
		// environment there is nothing for it to do.
		options.Redis = false
	}
	if !options.Database && servesLogin(options) && !options.Dynamo && !options.Firestore {
		// The login keeps ceremony records, an admission allowlist, and any
		// passkey credentials somewhere. Either non-relational store can hold
		// all of them; with none there is nowhere for them to go.
		return initOptions{}, fmt.Errorf(
			"init: --auth=%s needs a store for its login records; keep the database, or add --dynamo or --firestore",
			options.Auth)
	}
	if options.Dynamo && options.Firestore && servesLogin(options) && !options.Database {
		// auth.backend names one store for all four of its kinds, so a project
		// with both and no relational database has no defined winner. Asking is
		// better than picking one and reporting the choice.
		return initOptions{}, errors.New(
			"init: --dynamo and --firestore both hold the login records; keep the database, or choose one store")
	}
	if !options.Database && engineSelected {
		return initOptions{}, errors.New("init: --db selects the database engine; drop --no-database")
	}
	if !options.Database && sessionSelected && options.Session == sessionRDB {
		return initOptions{}, errors.New("init: --session=rdb needs the database; drop --no-database or choose another backend")
	}
	if !options.Database && !sessionSelected && options.Dynamo {
		// The default is relational, and this project has no relational
		// database. Following the store it does have beats scaffolding a
		// session pool nothing opens.
		options.Session = sessionDynamo
	}
	if !options.Database && !sessionSelected && options.Firestore && !options.Dynamo {
		options.Session = sessionFirestore
	}
	options = normalizeSession(options)
	return options, nil
}

// normalizeSession settles what the session backend implies. A Redis-backed
// session wants the development server that serves it, and a project without a
// login stores no sessions at all.
func normalizeSession(options initOptions) initOptions {
	if !servesBrowserLogin(options) {
		return options
	}
	if sessionBackend(options) == sessionRedis && options.Devbox {
		options.Redis = true
	}
	return options
}

// interactiveTerminal reports whether the wizard can drive the current session.
func interactiveTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

func runInit(args []string, stdout io.Writer) error {
	options, err := parseInitArgs(args)
	if err != nil {
		return err
	}
	// The wizard runs whether or not a name was given. A caller who knows the
	// name has still not answered the store, authentication, router, or
	// toolchain questions, and a question never asked is an option never
	// discovered. --yes is the way to say the flags are the whole answer, and a
	// session with no terminal is that case by construction.
	if !options.Yes && interactiveTerminal() {
		options, err = runInitWizard(options)
		if errors.Is(err, errWizardCanceled) {
			fmt.Fprintln(stdout, "init canceled")
			return nil
		}
		if err != nil {
			return err
		}
	} else if options.Name == "" {
		// Nothing can supply the name here: there is no wizard to ask in, and no
		// default worth guessing for a directory this command is about to create.
		return fmt.Errorf("init: a project name is required without the wizard; %s", initUsage)
	}
	// A package is named by the module path a consumer imports, and created in
	// the directory a checkout of that repository is called.
	name := options.Name
	if options.Kind == kindPackage {
		name = moduleDirectory(options.Name)
	}
	destination, err := initDestination(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	progress := newProgressRegion(stdout)
	progress.Phase("writing " + name)
	files := scaffoldFiles(options)
	for path, content := range files {
		target := filepath.Join(destination, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.Contains(content, "keyring.secret") {
			// The scaffolded development config carries a generated keyring
			// secret, which no other local user has any business reading.
			mode = 0o600
		}
		if err := writeScaffoldFileMode(target, []byte(content), mode); err != nil {
			return err
		}
	}
	progress.Phase("resolving modules")
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = destination
	tidy.Stdout = stdout
	tidy.Stderr = stdout
	tidy.Env = os.Environ()
	if err := tidy.Run(); err != nil {
		progress.Done()
		return fmt.Errorf("initialize Go module: %w", err)
	}
	progress.Phase("generating")
	previous, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(destination); err != nil {
		return err
	}
	generated, generateErr := generateProject(context.Background(), false, stdout, false)
	restoreErr := os.Chdir(previous)
	if generateErr != nil {
		progress.Done()
		return fmt.Errorf("generate starter: %w", generateErr)
	}
	if restoreErr != nil {
		progress.Done()
		return restoreErr
	}
	if options.Kind == kindPackage {
		// A package's only Go is the generated Go, so the tidy above ran over
		// an empty module and resolved none of the imports that generation was
		// about to write. An application has handwritten sources naming the
		// same packages before the first tidy, which is why it needs one run
		// and this needs two.
		progress.Phase("resolving generated imports")
		tidyGenerated := exec.Command("go", "mod", "tidy")
		tidyGenerated.Dir = destination
		tidyGenerated.Stdout = stdout
		tidyGenerated.Stderr = stdout
		tidyGenerated.Env = os.Environ()
		if err := tidyGenerated.Run(); err != nil {
			progress.Done()
			return fmt.Errorf("resolve generated imports: %w", err)
		}
	}
	progress.Done()
	fmt.Fprintf(stdout, "\nCreated %s\n", name)
	reportCreatedSources(stdout, files)
	if options.Kind == kindPackage {
		// The generated files are not the aside they are in an application:
		// they are what a consumer compiles, and committing them is the one
		// thing about this project kind somebody has to know.
		fmt.Fprintf(stdout, "\n%d generated files, which this project commits\n"+
			"  pw generate rebuilds them, and the scaffolded workflow fails when a commit is stale\n", generated)
		fmt.Fprintf(stdout, "\n  cd %s\n  git init && git add . && git commit\n", name)
		fmt.Fprint(stdout, "\npw dev and pw build do not apply to a package; go test is the loop\n")
		return nil
	}
	// The generated files are build inputs, named after sources the operator has
	// not read yet and excluded by the .gitignore this same scaffold wrote.
	// Listing them answers no question; saying how to remake them does.
	fmt.Fprintf(stdout, "\n%d generated files, rebuilt any time by pw generate\n", generated)
	// The wizard says this beside each answer, but a scripted run never sees
	// it, and a declined capability is the one thing about the new project an
	// operator may not know is reversible.
	if declined := declinedCapabilities(options); len(declined) > 0 {
		fmt.Fprintf(stdout, "\nNot included: %s\n  pw add <capability> enables one later\n",
			strings.Join(declined, ", "))
	}
	// The cookie backend seals its records under a secret the project cannot
	// invent for itself, so an operator has to supply one before the first run.
	if servesBrowserLogin(options) && sessionBackend(options) == sessionCookie {
		fmt.Fprint(stdout, "\nThe cookie session backend needs its sealing secret:\n"+
			"  export SESSION_COOKIE_SECRET=$(openssl rand -base64 32)\n")
	}
	if notice := databaseEngineNotice(options); notice != "" {
		fmt.Fprint(stdout, notice)
	}
	// Without the Devbox environment nothing pins the CSS toolchain, and the
	// first pw dev would fail on a binary the scaffold never installed.
	if options.Tailwind && !options.Devbox {
		fmt.Fprintf(stdout, "\nTailwind CSS needs its own toolchain here:\n  install %s\n",
			tailwindToolchainRequirement)
	}
	fmt.Fprintf(stdout, "\n  cd %s\n%s  pw dev\n", name, devboxNextStep(options))
	return nil
}

// reportCreatedSources names the handwritten files the scaffold wrote, grouped
// by the directory they landed in. These are the files the operator now owns,
// edits, and commits, which is what a report of a new project should be about.
func reportCreatedSources(stdout io.Writer, files map[string]string) {
	grouped := map[string][]string{}
	for path := range files {
		directory, file := "", path
		if cut := strings.LastIndex(path, "/"); cut >= 0 {
			directory, file = path[:cut], path[cut+1:]
		}
		grouped[directory] = append(grouped[directory], file)
	}
	directories := make([]string, 0, len(grouped))
	for directory := range grouped {
		directories = append(directories, directory)
	}
	// The project root sorts first as the empty string, which is where the
	// files an operator opens first happen to live.
	sort.Strings(directories)
	fmt.Fprintln(stdout)
	for _, directory := range directories {
		names := grouped[directory]
		sort.Strings(names)
		label := directory + "/"
		if directory == "" {
			label = "."
		}
		fmt.Fprintf(stdout, "  %-14s %s\n", label, strings.Join(names, "  "))
	}
}

// devboxNextStep names the shell to enter first, for a project that has one.
func devboxNextStep(options initOptions) string {
	if !options.Devbox {
		return ""
	}
	return "  devbox shell\n"
}

// declinedCapabilities names what this project did not take, in catalog order.
func declinedCapabilities(options initOptions) []string {
	var declined []string
	for _, capability := range capabilityOrder {
		switch capability {
		case capabilityDevbox:
			if !options.Devbox {
				declined = append(declined, capability)
			}
		case capabilityDatabase:
			if !options.Database {
				declined = append(declined, capability)
			}
		case capabilityDynamo:
			if !options.Dynamo {
				declined = append(declined, capability)
			}
		case capabilityFirestore:
			if !options.Firestore {
				declined = append(declined, capability)
			}
		case capabilityRedis:
			if !options.Redis {
				declined = append(declined, capability)
			}
		case capabilityAuth:
			if !servesLogin(options) {
				declined = append(declined, capability)
			}
		case capabilityTailwind:
			if !options.Tailwind {
				declined = append(declined, capability)
			}
		}
	}
	return declined
}

func writeScaffoldFile(target string, content []byte) error {
	return writeScaffoldFileMode(target, content, 0o644)
}

func writeScaffoldFileMode(target string, content []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, target)
}

func validProjectName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// initDestination resolves the project directory and refuses collisions.
func initDestination(name string) (string, error) {
	if !validProjectName(name) {
		return "", fmt.Errorf("invalid project name %q", name)
	}
	destination, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	entries, readErr := os.ReadDir(destination)
	switch {
	case readErr == nil && len(entries) > 0:
		return "", fmt.Errorf("destination %s is not empty", destination)
	case readErr != nil && !os.IsNotExist(readErr):
		return "", readErr
	}
	return destination, nil
}

// validateProjectName reports the wizard-facing reason a name is unusable.
func validateProjectName(name string) error {
	if name == "" {
		return errors.New("a project name is required")
	}
	_, err := initDestination(name)
	return err
}

func scaffoldFiles(options initOptions) map[string]string {
	if options.Kind == kindPackage {
		// A different project kind rather than a different set of answers:
		// there is no entry point, no document shell, no environment file, and
		// no capability to configure, so this shares the editor files and
		// nothing else.
		files := packageScaffoldFiles(options)
		mergeAgentSkillFiles(files, options)
		canonicalScaffoldSources(files)
		return files
	}
	name := options.Name
	moduleExtra := frameworkModuleDirective()
	// git is here because the Go toolchain shells out to it. go get and go
	// install resolve a module by running the version control system that
	// publishes it, so in a shell that pins Go and nothing else they fail on a
	// missing executable rather than on anything about the module. The
	// environment is a closed one — that is what pinning is for — so a git on
	// the host is not a git this shell can reach.
	devboxPackages := []string{"go@latest", "git@latest"}
	if options.Database {
		if server := engineFor(options.Engine).DevboxPackage; server != "" {
			devboxPackages = append(devboxPackages, server)
		}
	}
	if options.Dynamo {
		// The local server the scaffolded endpoint points at, added the way the
		// Valkey and the SQL servers are.
		devboxPackages = append(devboxPackages, dynamoDevboxPackage)
	}
	if options.Redis {
		devboxPackages = append(devboxPackages, "valkey@latest")
	}
	if options.TinyGo {
		devboxPackages = append(devboxPackages, "tinygo@latest")
	}
	configTailwind := ""
	configImages := ""
	// Declining Tailwind costs the utilities, not the page: the starter page is
	// styled either way, by the toolchain or by a stylesheet the application
	// owns from the moment it is written.
	// public/app.css is written either way. With Tailwind it carries the error
	// pages only, because those are framework-shaped and their class names are
	// not utilities; without it, it carries the starter page as well.
	// The stylesheet is named through AssetURL rather than as a literal path,
	// because a literal has no revision segment and revalidates on every load.
	// The argument is the path inside the served tree: the mount belongs to the
	// runtime configuration, and a template spelling it out would be a second
	// place to change when it moves.
	homeStylesheet := `<link rel="stylesheet" href={AssetURL("app.css")}>`
	homeClasses := ""
	if options.Images {
		// The encoders the image conversion runs. They are declared here so the
		// tool environment installs them, rather than being something a
		// developer discovers from a build that quietly converted nothing.
		devboxPackages = append(devboxPackages, imageDevboxPackages...)
	}
	if options.Images {
		configImages = imagesProjectConfig()
	}
	if options.Tailwind {
		configTailwind = tailwindProjectConfig()
		devboxPackages = append(devboxPackages, tailwindDevboxPackage)
		homeStylesheet += `<link rel="stylesheet" href={AssetURL("generated/app.css")}>`
		homeClasses = ` class="mx-auto max-w-3xl p-8 text-slate-900"`
	}
	files := map[string]string{
		"go.mod": "module " + name + "\n\ngo 1.27.0\n\n" + moduleExtra,
		"popcornweb.toml": `[project]
name = "` + name + `"
main = "./cmd/` + name + `"
toolchain = "` + projectToolchain(options) + `"
` + projectDatabaseConfig(options) + projectFastHTTPConfig(options) + `
# Each purpose reads only the directories it lists, and nothing else. A source
# directory is invisible to that purpose until it appears here.
[generate]
handlers = [` + quotedList(scaffoldGenerationScope(options).Handlers) + `]
templates = [` + quotedList(scaffoldGenerationScope(options).Templates) + `]
queries = [` + quotedList(scaffoldGenerationScope(options).Queries) + `]
config = [` + quotedList(scaffoldGenerationScope(options).Config) + `]
# A page tree root is one generation run: every directory below it holding a
# page template is a route, and the registration is generated.
pages = [` + quotedList(scaffoldGenerationScope(options).Pages) + `]
# The dynamo-tagged types and .pw.dynamo queries of the DynamoDB store, which is
# its own purpose because it shares no source kind with the SQL path.
dynamo = [` + quotedList(scaffoldGenerationScope(options).Dynamo) + `]
# The firestore-tagged types and .pw.firestore queries of the Firestore store,
# a purpose of its own for the same reason.
firestore = [` + quotedList(scaffoldGenerationScope(options).Firestore) + `]
# Point the compiler, the debugger, and the editor at the template line that
# produced a piece of generated Go, instead of at the generated file nobody
# wrote. It costs go test -cover: with this on, a coverage profile names lines
# that do not exist in the file it reports them against. Take one or the other.
line_directives = false

# API handlers also speak application/cbor beside JSON: a request carrying it
# binds from one CBOR map, and a response answers CBOR when Accept names it
# outright. Settled at generation time; left off, no CBOR code is linked.
# reject_floats refuses float64 fields for scaled-integer schemas, and
# sorted_keys emits RFC 8949 bytewise key order for deterministic clients.
[generate.api.cbor]
enabled = false

# pw dev walks the module for rebuild inputs. Add what the walk misses, and
# exclude a subtree that only makes the walk slower.
[dev.watch]
includes = []
excludes = []

# pw dev keeps the terminal readable and also writes structured application
# records here for local analysis. One JSONL file is created per pw dev run.
[dev.logs]
enabled = true
directory = ".log"
` + devConsoleProjectConfig() + devIdPProjectConfig(options) + configTailwind + configImages,
		pwenv.FileName(pwenv.Development): `# Development runtime configuration.
# APP_ENV selects this file; add config.stg.toml and config.prod.toml as needed.
[server]
port = 8080
# Operational endpoints answer only at the paths named here; an unset key
# serves nothing, so this file lists every address the server responds on.
health = "/healthz"
readiness = "/readyz"
openapi = "/openapi.json"
# Scalar API reference for the document above, served at server.api_doc_path
# (/docs). Leave this key out of staging and production configs to keep the UI
# private.
api_doc = "scalar"
` + apiCatalogRuntimeConfig(options) + `
[observability]
minimum_level = "debug"
service_name = "` + name + `"
# The slog text encoding, which is what a terminal reads. An unset key already
# resolves to this in dev and to "json" everywhere else; it is written out here
# so the format this file produces is visible rather than inferred.
stdout_format = "plaintext"
` + databaseRuntimeConfig(options) + dynamoRuntimeConfig(options) + firestoreRuntimeConfig(options) + sessionRuntimeConfig(options) + authRuntimeConfig(options) + securityRuntimeConfig(options),
		"cmd/" + name + "/main.go": mainScaffold(options),
		"templates/document.pw.html": `package templates

external AssetURL(name: string): url
external RuntimeScriptURL(): url

export component Document(children: html?): html {
  <!doctype html>
  <html lang="en"><head><meta charset="utf-8"><title>Popcorn Web</title>` + homeStylesheet +
			`<script type="module" src={RuntimeScriptURL()}></script></head>
  <body` + homeClasses + `><slot /></body></html>
}
`,
		"templates/templates.go": `package templates

import (
	"net/url"

	"github.com/shibukawa/popcornweb/pw"
)

// RuntimeScriptURL backs the external declaration in document.pw.html.
//
// The runtime it names applies the sections of a page that arrive after the
// rest of it, which is what a template declaring an ` + "`async`" + ` parameter needs.
// A page without one loads it and finds nothing to do.
//
// The template calls this rather than writing a literal path, because the URL
// carries a revision derived from the script's own bytes: an upgrade that
// changes the runtime changes the URL, and a literal would go on pointing at
// bytes the server no longer serves.
func RuntimeScriptURL() *url.URL { return &url.URL{Path: pw.RuntimeScriptURL()} }

// AssetURL backs the external declaration in document.pw.html.
//
// It takes the path of a file inside the served tree — "app.css", not
// "/public/app.css" — and returns the URL this build serves it under. That URL
// carries a revision derived from the file's own bytes, so a deployment can
// answer it immutably: a browser holding it never asks again, and an edit
// changes the URL rather than the answer behind it.
//
// A literal path still works and still resolves. It just has no revision, so
// the browser revalidates it on every page load.
//
// Use it for the assets nothing else renames: a stylesheet, a plain script. An
// ` + "`img src`" + ` and a TypeScript ` + "`script src`" + ` are rewritten by the build
// already, into names that carry their own digest, and writing those through
// this function would hide them from the conversion that does it.
func AssetURL(name string) *url.URL { return &url.URL{Path: pw.PublicAssetURL(name)} }
`,
		"templates/errors.go":   errorRegistrationScaffold(),
		"templates/400.pw.html": errorTemplate("templates", "Error400", "Bad Request"),
		"templates/401.pw.html": errorTemplate("templates", "Error401", "Unauthorized"),
		"templates/403.pw.html": errorTemplate("templates", "Error403", "Forbidden"),
		"templates/404.pw.html": errorTemplate("templates", "Error404", "Not Found"),
		"templates/409.pw.html": errorTemplate("templates", "Error409", "Conflict"),
		"templates/413.pw.html": errorTemplate("templates", "Error413", "Payload Too Large"),
		"templates/429.pw.html": errorTemplate("templates", "Error429", "Too Many Requests"),
		"templates/500.pw.html": errorTemplate("templates", "Error500", "Internal Server Error"),
		"public.go": `package publicassets

import (
	"embed"
	"io/fs"

	"github.com/shibukawa/popcornweb/middlewares"
)

//go:embed all:dist/public
var embeddedPublic embed.FS

func init() {
	middlewares.RegisterPublicFS(PublicFS())
}

func PublicFS() fs.FS {
	result, err := fs.Sub(embeddedPublic, "dist/public")
	if err != nil {
		panic(err)
	}
	return result
}
`,
		"public/.keep": "",
		// The tree that ships beside the binary rather than inside it. The
		// sentinel is here for the container COPY rather than for go:embed:
		// nothing embeds this directory, and a COPY of a path that does not
		// exist fails the image build.
		externalPublicDir + "/.keep": "",
		// go:embed fails on an absent directory, so a project that has never
		// run a build still has a tree to embed. The build replaces it.
		"dist/public/.keep": "",
		".vscode/settings.json": `{
    "files.exclude": {
        "**/*_pw_gen.go": true
    }
}
`,
		".vscode/extensions.json": editorExtensionsScaffold(options),
		".editorconfig":           editorConfigScaffold(),
		// The environment file for anywhere this project is deployed. Resolution
		// takes the first readable candidate and stops, so it is a whole
		// configuration rather than a diff on the development one.
		pwenv.FileName(pwenv.Production): productionConfigScaffold(options),
		// The binary pattern is anchored: a bare name would also ignore cmd/<name>/.
		//
		// devbox.d is tracked, and no line here excludes it. Devbox writes a
		// service's configuration exactly once — on the run that first resolves
		// the package and stamps plugin_version into devbox.lock — and from then
		// on treats the file as the developer's to edit and refuses to recreate
		// it. devbox.lock is committed, so ignoring devbox.d leaves the author
		// with a working service and everyone who clones with a valkey-server
		// that exits on "can't open config file". It is generated output, but
		// unlike *_pw_gen.go nothing regenerates it, and a commit is the only
		// thing that can carry it to the next checkout.
		".gitignore": ".devbox/\n.log/\n.pw/\n/" + name + "\n*_pw_gen.go\n" +
			// The generated subtree of the authored public tree answers to the
			// same test as the line above, and fails it the same way: pw generate
			// rebuilds every file in it from the CSS entry, the templates, and the
			// scanned Go. Two producers write here — the Tailwind output and the
			// content-hashed component assets the generator extracts — and both
			// track their sources, so a committed copy is a copy that drifts. It
			// costs a stale stylesheet in the best case and, because an extracted
			// name carries a content hash, an orphan per change in the worst.
			//
			// A package inverts this, as it inverts *_pw_gen.go, because its
			// consumer builds with go build and regenerates nothing. See
			// decision:generated-public-asset-version-control.
			extractedAssetDir + "/\n" +
			// Everything under dist is built, except the sentinel: go:embed
			// fails on an absent directory, so a fresh clone has to carry one
			// file that makes the tree exist before the first build.
			"dist/cache/\ndist/derived/\ndist/manifest.json\ndist/routes.json\ndist/public/*\n!dist/public/.keep\n*.db\n",
	}
	for path, source := range containerScaffoldFiles(options) {
		files[path] = source
	}
	if routerHasRegistered(options.Router) {
		for path, source := range registeredRouterScaffold(options, defaultRegisteredDir) {
			files[path] = source
		}
	}
	if routerHasDiscovered(options.Router) {
		for path, source := range pageTreeScaffold(options, defaultDiscoveredDir) {
			files[path] = source
		}
	}
	for path, source := range secondBuildFiles(options) {
		files[path] = source
	}
	if options.Devbox {
		files["devbox.json"] = devboxScaffold(devboxPackages)
		files["devbox.lock"] = "{}\n"
	}
	files["public/app.css"] = applicationStylesheet(options)
	if options.Dynamo {
		files[defaultDynamoDir+"/note.go"] = dynamoRecordScaffold()
		files[defaultDynamoDir+"/notes.pw.dynamo"] = dynamoQueryScaffold()
	}
	if options.Firestore {
		files[defaultFirestoreDir+"/note.go"] = firestoreEntityScaffold()
		files[defaultFirestoreDir+"/notes.pw.firestore"] = firestoreQueryScaffold()
	}
	if options.Database {
		files["queries/users.pw.sql"] = starterQuery()
		files["migrations/00001_init.sql"] = engineFor(options.Engine).Schema
	}
	if options.TinyGo {
		files["tinygohelper.go"] = `//go:build tinygo

package publicassets

// TinyGo's net package routes every socket through a Netdever that the program
// has to register itself; without one the server dies at startup with
// "Netdev not set". The blank import registers the host OS driver during init.
// Standard Go builds skip this file and use the real net package.
import _ "github.com/shibukawa/tinygodriver/netdev"
`
	}
	if options.Tailwind {
		files[defaultTailwindInput] = tailwindEntryScaffold(scaffoldGenerationScope(options))
		files[defaultTailwindOutput] = "/* Generated by Tailwind CSS. */\n"
	}
	// The emulator is an OpenID Provider, so a mode without one never gets a
	// roster even if the option survived from an earlier answer.
	if options.AuthEmulator && usesOIDC(options.Auth) {
		files[defaultIdPConfig] = devIdPRoster()
	}
	if usesPasskey(options.Auth) {
		files["public/passkey.js"] = passkeyBrowserScaffold(options)
	}
	if servesBrowserLogin(options) {
		files["public/presence.js"] = presenceBrowserScaffold()
	}
	if servesBrowserLogin(options) {
		files["handlers/accounts.go"] = accountsScaffold(options)
		// The framework tables come from the packages that own them. A fresh
		// project has only the application schema, so these take the versions
		// after it; pw add would take whatever is free at that point instead.
		// Only the rdb backend owns a table: a cookie or Redis session leaves
		// the numbering to the auth migration alone.
		version := 2
		dialect := engineDialect(options.Engine)
		if sessionBackend(options) == sessionRDB {
			// A migration is written in the dialect of the engine this project
			// selected, because no engine reads another's DDL.
			migration, err := sessionstore.MigrationSQL(dialect, "popcornweb_session")
			if err != nil {
				panic(err)
			}
			files["migrations/00002_"+sessionstore.MigrationName+".sql"] = migration
			version = 3
		}
		if authBackend(options) == "rdb" {
			// A non-relational backend has no migration file to write. The
			// DynamoDB tables are the desired state pw migrate assembles from
			// the registered definitions, and a Firestore kind is created by
			// the first write, so neither has one.
			authMigration, err := auth.MigrationSQL(dialect)
			if err != nil {
				panic(err)
			}
			files[fmt.Sprintf("migrations/0000%d_%s.sql", version, auth.MigrationName)] = authMigration
		}
	}
	mergeAgentSkillFiles(files, options)
	canonicalScaffoldSources(files)
	return files
}

// servesLogin reports whether the framework mounts authentication endpoints for
// this project.
// servesLogin reports whether the framework mounts authentication endpoints for
// this project. An unset answer reads as none, the way effectiveRouter reads an
// unset router: a caller that never asked gets the shape it had before the
// question existed.
func servesLogin(options initOptions) bool {
	return options.Auth != "" && options.Auth != authNone
}

// databaseRuntimeConfig writes the rdb section the scaffolded migrations and
// queries need. A project that declined the database has neither, so the
// section would configure a pool nothing opens.
func databaseRuntimeConfig(options initOptions) string {
	if !options.Database {
		return ""
	}
	engine := engineFor(options.Engine)
	return databaseRuntimeSection(pwenv.Development, engine.DSN(options.Name), engine)
}

// databaseRuntimeSection is the rdb configuration api:cli-init scaffolds and
// api:cli-add appends, so both reach the same file state. The pool bounds come
// from the engine: one connection is right for a file SQLite writes serially
// and wrong for a server that expects a pool.
//
// One element is one pool, and it is the only form the section has. A project
// that grows a replica adds a second element and names the groups above them;
// nothing about the first element changes.
//
// devDSN is the local database the scaffolded migrations run against, and it is
// written only into the development file. A deployed environment names no
// database here: the element carries a ${DATABASE_URL} reference instead,
// because an array element has no environment variable of its own and the value
// belongs to the deployment rather than to the repository.
func databaseRuntimeSection(env, devDSN string, engine databaseEngine) string {
	dsn := `dsn = "` + devDSN + `"`
	if env != pwenv.Development {
		dsn = `# The deployment supplies this. ${NAME} is expanded when the file loads, and
# an undefined name is a load error rather than an empty DSN.
dsn = "${DATABASE_URL}"`
	}
	return `
# The scaffolded migrations and queries need a database; pw dev and pw migrate
# read this connection.
[middleware.rdb]
enabled = true

# One element is one pool. The group is the name statements address it by, and
# a single connection answers to every group name, so this one stays "default"
# until a second database arrives.
[[middleware.rdb.connections]]
group = "default"
` + dsn + `
connect_timeout = "5s"
max_open_conns = ` + strconv.Itoa(engine.MaxOpenConns) + `
max_idle_conns = ` + strconv.Itoa(engine.MaxIdleConns) + `
`
}

// dynamoRuntimeConfig is the middleware.dynamo section. It is independent of
// middleware.rdb: a project may have either, both, or neither, because DynamoDB
// is a second kind of store rather than another SQL engine.
func dynamoRuntimeConfig(options initOptions) string {
	if !options.Dynamo {
		return ""
	}
	return dynamoRuntimeSection()
}

// dynamoRuntimeSection is what api:cli-init scaffolds and api:cli-add appends,
// so both reach the same file state. The credentials are development values:
// the local server does not verify a signature, and the region is a placeholder
// it accepts.
func dynamoRuntimeSection() string {
	return `
# The DynamoDB store, independent of middleware.rdb. These values point at the
# amazon/dynamodb-local server pw dev starts; deployment supplies its own.
[middleware.dynamo]
enabled = true
region = "us-east-1"
endpoint = "http://127.0.0.1:8000"
access_key_id = "local"
secret_access_key = "local"
# Development creates tables from the registered definitions. A deployed table
# comes from deployment tooling, so this key is a configuration error outside dev.
auto_migrate = true
`
}

// firestoreRuntimeConfig is the middleware.firestore section. It is
// independent of middleware.rdb and of middleware.dynamo: a project may have
// any combination, because each is its own kind of store.
func firestoreRuntimeConfig(options initOptions) string {
	if !options.Firestore {
		return ""
	}
	return firestoreRuntimeSection()
}

// firestoreRuntimeSection is what api:cli-init scaffolds and api:cli-add
// appends, so both reach the same file state.
//
// No credential is configured. The endpoint points at the local emulator, which
// ignores the Authorization header entirely, so a key here would be pretending
// to exercise the token path. A deployment names its own credential source, and
// on Cloud Run or GKE that is credentials = "metadata".
func firestoreRuntimeSection() string {
	return `
# The Firestore store, in Datastore mode, independent of middleware.rdb. These
# values point at the local Datastore emulator, which you start with
#
#   gcloud beta emulators datastore start --host-port=127.0.0.1:8081
#
# The database a deployment names must have been created in Datastore mode:
# the mode is chosen at creation and cannot be changed afterwards.
[middleware.firestore]
enabled = true
project_id = "demo-popcornweb"
endpoint = "127.0.0.1:8081"
`
}

// firestoreEntityScaffold is the starter bound entity.
//
// The key is lifted out of the properties, which is the one thing a reader
// coming from the DynamoDB store has to unlearn: Datastore keeps identity
// beside the entity rather than among it, so an identifier field carries no
// property name.
func firestoreEntityScaffold() string {
	return `package entities

import (
	"context"
	"time"

	"github.com/shibukawa/popcornweb/database/firestore"
	"github.com/shibukawa/tinybind-go/firestorebind"
)

// Note is stored in Firestore, in Datastore mode. Its kind is the Go type name,
// which is why nothing here or in the declarations names one.
//
// ID is the key's name and is absent from the properties: Datastore stores a
// key beside them, so writing it as a property too would store identity twice.
//
// ExpiresAt is written as an ordinary timestamp and produces one generated
// fact: this kind's TTL policy targets "expires_at". Applying that policy is a
// deployment step, because Datastore mode has no expiry on the wire.
type Note struct {
	ID        string    ` + "`firestore:\"-,name\"`" + `
	Author    string    ` + "`firestore:\"author\"`" + `
	Body      string    ` + "`firestore:\"body,noindex\"`" + `
	CreatedAt time.Time ` + "`firestore:\"created_at\"`" + `
	ExpiresAt time.Time ` + "`firestore:\"expires_at,ttl\"`" + `
}

// The generator emits a codec only for the directions something actually uses,
// so these two calls are what make EncodeEntity, DecodeEntity, and EntityKey
// appear beside this file. Delete them and the generated code shrinks to match.
//
// firestore.Handle returns the process client bound to the configured
// namespace, so no context value stands between a call and the store. A
// declared .pw.firestore query resolves the same handle itself.
func StoreNote(ctx context.Context, note Note) error {
	h, err := firestore.Handle(ctx)
	if err != nil {
		return err
	}
	_, err = h.Store(ctx, note)
	return err
}

func LoadNote(ctx context.Context, id string) (Note, error) {
	h, err := firestore.Handle(ctx)
	if err != nil {
		return Note{}, err
	}
	return h.Load[Note](ctx, Note{ID: id}.EntityKey())
}
`
}

// firestoreQueryScaffold is the starter access pattern.
//
// A declaration names no kind: the result type names the Go type, and that
// type's generated Kind method is the kind, so a declaration cannot disagree
// with the codec about what it is querying.
func firestoreQueryScaffold() string {
	return `// Access patterns for Note. Every property here is checked against the firestore
// tags on the Go type, so a renamed tag fails generation rather than returning
// an empty batch.

export statement NotesByAuthor(author: string): firestore.many<Note> {
  where author == {author}
}
`
}

// dynamoRecordScaffold is the starter typed record. Its table is created from
// the generated definition rather than from a migration file, because the
// DynamoDB schema is the set of registered tables and has no version sequence.
func dynamoRecordScaffold() string {
	return `package records

import (
	"context"
	"time"

	"github.com/shibukawa/popcornweb/database/dynamo"
	"github.com/shibukawa/tinybind-go/dynamobind"
)

// A dynamo-tagged struct declares a table. pw generate emits its item codec,
// its key builder, and the table definition that creates the table in
// development, all beside this file and none of it by hand.
//
// There is no migration file for this: the DynamoDB schema is the set of
// registered table definitions, so a table is added by declaring a type rather
// than by adding a version.
type Note struct {
	ID        string    ` + "`dynamo:\"id,partitionkey\"`" + `
	CreatedAt time.Time ` + "`dynamo:\"created_at,sortkey\"`" + `
	Body      string    ` + "`dynamo:\"body\"`" + `
}

// The generator emits a codec only for the directions something actually uses,
// so these two calls are what make EncodeItem, DecodeItem, and ItemKey appear
// beside this file. Delete them and the generated code shrinks to match.
//
// dynamo.Handle returns the process client bound to the configured table
// naming, so no context value stands between a call and the store. A declared
// .pw.dynamo query resolves the same handle itself.
func StoreNote(ctx context.Context, note Note) error {
	h, err := dynamo.Handle(ctx)
	if err != nil {
		return err
	}
	return h.Store(ctx, "note", note)
}

func LoadNote(ctx context.Context, id string, createdAt time.Time) (Note, error) {
	h, err := dynamo.Handle(ctx)
	if err != nil {
		return Note{}, err
	}
	return h.Load[Note](ctx, "note", Note{ID: id, CreatedAt: createdAt}.ItemKey())
}
`
}

// dynamoQueryScaffold is the starter access pattern, which the Firestore
// scaffold has had and this one had not.
//
// It is not decoration beside the type: a declaration is a use of its result
// type, so it is what makes the read side of the codec exist for a package that
// declares one. Its call site names neither the table nor the client, which is
// the whole point of declaring it.
func dynamoQueryScaffold() string {
	return `// Access patterns for Note. Every attribute here is checked against the dynamo
// tags on the Go type, so a renamed tag fails generation rather than returning
// an empty page.

export statement NotesSince(id: string, from: time.Time): dynamo.many<Note> {
  table note
  key id = {id} and created_at > {from}
}
`
}

// authBackend names the storage of the tables plugin/auth owns. It follows the
// store the login was built on: a project with no relational database keeps
// them in DynamoDB, and every other project keeps the relational default.
func authBackend(options initOptions) string {
	if servesBrowserLogin(options) && !options.Database {
		switch {
		case options.Dynamo:
			return "dynamo"
		case options.Firestore:
			return "firestore"
		}
	}
	return "rdb"
}

// sessionBackendImport contributes the storage the configuration selects.
// Session storage is opt-in by blank import, so an application links the one
// backend it configured and nothing else. Cookie and both development intent
// modes are built into pw and need no line here.
func sessionBackendImport(options initOptions) string {
	imports := ""
	if plugin := sessionBackendPlugin(sessionBackend(options), options.Engine); plugin != "" {
		imports += "\n\t// session.backend = \"" + sessionBackend(options) +
			"\" is served by this import; storage is opt-in.\n\t_ " + strconv.Quote(plugin)
	}
	if options.Auth == authJWTOnly {
		// The bearer mode registers no account seam, so nothing else in this
		// project would reach plugin/auth. Without the import the package is
		// not linked, its extension never registers, and the [auth] section
		// this scaffold wrote is read as plain keys nobody validates: startup
		// accepts an empty issuer and every request arrives unauthenticated.
		imports += "\n\t// The bearer verifier. It registers itself on import, and without\n" +
			"\t// this line the [auth] section below is configuration nothing reads.\n\t_ " +
			strconv.Quote("github.com/shibukawa/popcornweb/plugin/auth")
	}
	if servesBrowserLogin(options) {
		if backend := authBackend(options); backend != "rdb" {
			// A non-relational auth.backend moves all four stores plugin/auth
			// owns, so both halves are imported: the ceremony store and the
			// account-side stores.
			imports += "\n\t// auth.backend = \"" + backend + "\" is served by these two imports.\n\t_ " +
				strconv.Quote("github.com/shibukawa/popcornweb/authstate/"+backend) +
				"\n\t_ " + strconv.Quote("github.com/shibukawa/popcornweb/authstore/"+backend)
		} else {
			// The login ceremony records live in the database whichever backend
			// holds the sessions, so their engine is imported for every login.
			imports += "\n\t// The single-use login records this engine stores.\n\t_ " +
				strconv.Quote("github.com/shibukawa/popcornweb/authstate/"+engineDialect(options.Engine))
		}
	}
	return imports
}

// projectDatabaseConfig records the engine .pw.sql sources are generated for.
// A project without a database writes no key, because there is nothing to
// generate and the default would be an answer to a question it never asked.
func projectDatabaseConfig(options initOptions) string {
	if !options.Database {
		return ""
	}
	return "database = \"" + options.Engine + "\"\n"
}

// projectFastHTTPConfig records the second backend. The key is written only
// when it was taken: an absent key and a false one mean the same thing, and a
// project that never answered the question should not carry an answer to it.
func projectFastHTTPConfig(options initOptions) string {
	if !options.FastHTTP {
		return ""
	}
	return "# Build for fasthttp as well as net/http. Generated files importing\n" +
		"# net/http carry a !fasthttp constraint, and pw generate derives the second\n" +
		"# build's handlers and page tree from the net/http source.\n" +
		"fasthttp = true\n"
}

// databaseDriverImport links the selected engine into the application binary.
// Every engine is opt-in, so an application that declares no relational
// database carries no driver; without the selected import the pool refuses to
// open and names the import to add.
func databaseDriverImport(options initOptions) string {
	if !options.Database {
		return ""
	}
	path := engineFor(options.Engine).DriverImport
	if path == "" {
		return ""
	}
	return "\n\t// Registers the engine the configured DSN names.\n\t_ " + strconv.Quote(path)
}

// storeMiddlewareImport contributes the client a non-relational store needs.
//
// Only Firestore is written here. A DynamoDB project reaches database/dynamo
// through the generated table registration, and this package generates nothing
// for Firestore: a kind is created by the first write, so there is no
// definition to register and nothing else would carry the import.
func storeMiddlewareImport(options initOptions) string {
	if !options.Firestore {
		return ""
	}
	return "\n\t// Opens the Firestore client and installs it into every request.\n\t_ " +
		strconv.Quote("github.com/shibukawa/popcornweb/database/firestore")
}

// authBootstrap installs the account resolver. That call is the whole
// application-side wiring of a login: it also imports plugin/auth, whose
// extensions serve the endpoints and resolve the session.
func authBootstrap(options initOptions) string {
	if !servesBrowserLogin(options) {
		return ""
	}
	return "\n\t// Installed before Run: the framework calls these while it serves a login.\n\thandlers.RegisterAccounts()"
}

// passkeyBrowserScaffold is the browser half of a ceremony. The framework
// serves the endpoints but cannot run navigator.credentials for the page, so a
// project needs this much script; it has no dependencies and is meant to be
// read and replaced.
func passkeyBrowserScaffold(options initOptions) string {
	bootstrap := ""
	if options.Auth == authPasskey {
		bootstrap = `
// redeemBootstrap trades an administrator-issued login ID and one-time secret
// for one restricted enrollment. It creates no session: finishing the
// registration is what signs the account in.
export async function redeemBootstrap(loginId, secret) {
  await post("/auth/passkey/bootstrap", { login_id: loginId, secret });
  return register();
}

wire("passkey-bootstrap", (form) => {
  const data = new FormData(form);
  return redeemBootstrap(data.get("login_id"), data.get("secret"));
});
`
	}
	return `// Passkey ceremonies, driven from the page.
//
// The framework serves /auth/passkey/*; this file only converts between the
// Base64url the endpoints speak and the ArrayBuffers the WebAuthn API wants,
// which is the whole reason a script is needed at all.

const decode = (value) => {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  return Uint8Array.from(binary, (c) => c.charCodeAt(0));
};

const encode = (buffer) =>
  btoa(String.fromCharCode(...new Uint8Array(buffer)))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");

async function post(path, body) {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    // The endpoints are same-origin only, and a session cookie has to travel.
    credentials: "same-origin",
    body: JSON.stringify(body ?? {}),
  });
  if (!response.ok) {
    throw new Error("passkey: " + path + " failed with " + response.status);
  }
  return response.json();
}

// register adds a passkey to the account this browser is already allowed to
// enroll for.
export async function register() {
  const options = await post("/auth/passkey/register/begin");
  const credential = await navigator.credentials.create({
    publicKey: {
      ...options,
      challenge: decode(options.challenge),
      user: { ...options.user, id: decode(options.user.id) },
      excludeCredentials: (options.excludeCredentials ?? []).map((c) => ({
        ...c,
        id: decode(c.id),
      })),
    },
  });
  return post("/auth/passkey/register/finish", {
    id: credential.id,
    rawId: encode(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: encode(credential.response.clientDataJSON),
      attestationObject: encode(credential.response.attestationObject),
      transports: credential.response.getTransports?.() ?? [],
    },
  });
}

// login signs in with a passkey. No user name is asked for: the credential
// itself names the account.
export async function login() {
  const options = await post("/auth/passkey/login/begin");
  const assertion = await navigator.credentials.get({
    publicKey: {
      ...options,
      challenge: decode(options.challenge),
      allowCredentials: (options.allowCredentials ?? []).map((c) => ({
        ...c,
        id: decode(c.id),
      })),
    },
  });
  return post("/auth/passkey/login/finish", {
    id: assertion.id,
    rawId: encode(assertion.rawId),
    type: assertion.type,
    response: {
      clientDataJSON: encode(assertion.response.clientDataJSON),
      authenticatorData: encode(assertion.response.authenticatorData),
      signature: encode(assertion.response.signature),
      userHandle: assertion.response.userHandle
        ? encode(assertion.response.userHandle)
        : undefined,
    },
  });
}

// wire binds a control by id, so the page needs no inline script and the
// template stays free of JavaScript.
function wire(id, run) {
  const element = document.getElementById(id);
  if (!element) return;
  const event = element.tagName === "FORM" ? "submit" : "click";
  element.addEventListener(event, async (e) => {
    e.preventDefault();
    try {
      await run(element);
      location.reload();
    } catch (error) {
      const status = document.getElementById("passkey-status");
      if (status) status.textContent = String(error.message ?? error);
    }
  });
}

wire("passkey-login", login);
wire("passkey-register", register);
` + bootstrap
}

// accountsScaffold wires the account seams the selected mode needs. Each one is
// a small function the framework calls; the application owns account storage.
func accountsScaffold(options initOptions) string {
	imports := "\t\"context\"\n\n\t\"github.com/shibukawa/popcornweb/plugin/auth\"\n"
	body := "// RegisterAccounts installs the account seams. Call it from main before\n// pw.Run.\nfunc RegisterAccounts() {\n"
	if usesOIDC(options.Auth) {
		body += "\tauth.SetAccountResolver(resolveAccount)\n"
	}
	if usesPasskey(options.Auth) {
		body += "\tauth.SetAccountLookup(lookupAccount)\n"
	}
	if options.Auth == authPasskey {
		body += "\tauth.SetAccountActivator(activateAccount)\n"
	}
	body += "}\n"
	if usesOIDC(options.Auth) {
		body += `
// resolveAccount answers with the account behind a verified identity.
//
// This starter derives one instead of storing it, which is enough to log in
// and read the user. Replace it with a lookup against your own table as soon
// as the application owns accounts: the link is the issuer plus the verified
// claim auth.oidc.identity_claim selected, never the email address.
func resolveAccount(ctx context.Context, identity auth.Identity, provision bool) (auth.Account, error) {
	displayName, _ := identity.Claims.String("name")
	if displayName == "" {
		displayName = identity.Key
	}
	email, _ := identity.Claims.String("email")
	return auth.Account{
		ID:          identity.Issuer + "|" + identity.Key,
		DisplayName: displayName,
		Email:       email,
	}, nil
}
`
	}
	if usesPasskey(options.Auth) {
		body += `
// lookupAccount answers with the account behind a stable identifier.
//
// A passkey assertion resolves a credential to an account ID, which is the
// opposite direction from resolveAccount, so the framework asks here instead.
// Replace it with a read from your own table; returning the identifier alone
// is enough to authenticate but shows the user no name.
func lookupAccount(ctx context.Context, accountID string) (auth.Account, error) {
	return auth.Account{ID: accountID, DisplayName: accountID}, nil
}
`
	}
	if options.Auth == authPasskey {
		body += `
// activateAccount marks a provisional account usable. The framework runs it
// inside the transaction that persists the first passkey, so an account never
// becomes active without a credential and never gains a credential without
// becoming active. Replace the body with your own UPDATE.
func activateAccount(ctx context.Context, accountID string) error {
	return nil
}

// IssueFirstPasskey provisions the login ID and one-time secret that open one
// passkey enrollment, and returns the secret for delivery out of band.
//
// The secret is returned exactly once and is never stored; only its digest is.
// Put this behind administrator authorization before exposing it: anyone who
// can call it can enroll a credential for any account.
func IssueFirstPasskey(ctx context.Context, loginID, accountID string) (string, error) {
	return auth.IssueBootstrapCredential(ctx, loginID, accountID, auth.PurposeInitialPasskey)
}
`
	}
	return "package handlers\n\nimport (\n" + imports + ")\n\n" + body
}

// homeHandlerScaffold renders the starter page. With authentication it reads
// the signed-in user; the login itself belongs to the framework.
func homeHandlerScaffold(options initOptions) string {
	project := strconv.Quote(options.Name)
	pattern, _ := registeredHomeRoute(options)
	if !servesBrowserLogin(options) {
		return firstBuild(options) + `package handlers

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
)

func init() { mux.HandleFunc("` + pattern + `", home) }

// The comment below is not decoration. pw generate reads a handler's godoc into
// the OpenAPI document this project serves: the first sentence becomes the
// operation summary and the rest its description, and the field comments in
// homeInput describe the parameters. Write them and /docs explains the route;
// leave them out and it lists a path and nothing else.
//
// This paragraph is separated by a blank line, so it is not part of that godoc
// and does not reach the document.

// home renders the starter landing page.
//
// The greeting is whoever the request names, and the project the page was
// scaffolded for otherwise.
func home(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[homeInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	pw.WriteHTML(w, r, Home(HomeParams{Name: input.Name, Project: ` + project + `}))
}
`
	}
	return firstBuild(options) + `package handlers

import (
	"net/http"
	"net/url"

	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/pw"
)

func init() { mux.HandleFunc("` + pattern + `", home) }

// The comment below is not decoration. pw generate reads a handler's godoc into
// the OpenAPI document this project serves: the first sentence becomes the
// operation summary and the rest its description. Write them and /docs explains
// the route; leave them out and it lists a path and nothing else.
//
// This paragraph is separated by a blank line, so it is not part of that godoc
// and does not reach the document.

// home renders the starter landing page, signed in or not.
//
// A signed-in visitor is greeted by display name and offered a sign-out
// control; everyone else is offered the ways in that this project configured.
func home(w http.ResponseWriter, r *http.Request) {
	// The framework resolved the session before this handler ran.
	user, signedIn := auth.User(r.Context())
	name := "World"
	if signedIn {
		name = user.DisplayName
		if name == "" {
			name = user.Subject
		}
	}
	pw.WriteHTML(w, r, Home(HomeParams{
		Name:        name,
		Project:     ` + project + `,
		SignedIn:    signedIn,
		Email:       user.Email,
		LoginPath:   url.URL{Path: "/auth/login"},
		LogoutPath:  url.URL{Path: "/auth/logout"},
		Passkey:     ` + passkeyLiteral(usesPasskey(options.Auth)) + `,
		ProviderLogin: ` + passkeyLiteral(usesOIDC(options.Auth)) + `,
		Bootstrap:   ` + passkeyLiteral(options.Auth == authPasskey) + `,
	}))
}
`
}

// passkeyLiteral renders a Go bool literal for the scaffold.
func passkeyLiteral(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// landingStyle names the class of each element on the starter page. Tailwind
// selected means utilities; declined means the class names the scaffolded
// stylesheet defines. The two tables exist so there is one template rather than
// one per answer, because two page scaffolds would drift apart.
type landingStyle struct {
	Page    string
	Eyebrow string
	Title   string
	Lead    string
	Section string
	Heading string
	List    string
	Link    string
	Note    string
}

func landingStyleFor(options initOptions) landingStyle {
	if options.Tailwind {
		return landingStyle{
			Page:    ` class="space-y-10"`,
			Eyebrow: ` class="text-sm font-semibold uppercase tracking-widest text-indigo-600"`,
			Title:   ` class="mt-1 text-4xl font-bold tracking-tight"`,
			Lead:    ` class="mt-3 text-slate-600"`,
			Section: ` class="rounded-xl border border-slate-200 p-6"`,
			Heading: ` class="text-lg font-semibold"`,
			List:    ` class="mt-3 space-y-2 text-slate-700"`,
			Link:    ` class="font-medium text-indigo-600 underline underline-offset-2"`,
			Note:    ` class="text-sm text-slate-500"`,
		}
	}
	return landingStyle{
		Page:    ` class="page"`,
		Eyebrow: ` class="eyebrow"`,
		Title:   ` class="title"`,
		Lead:    ` class="lead"`,
		Section: ` class="card"`,
		Heading: ` class="card-heading"`,
		List:    ` class="card-list"`,
		Link:    ` class="link"`,
		Note:    ` class="note"`,
	}
}

// landingStylesheet is the application-owned CSS a project that declined
// Tailwind gets. Declining the toolchain should cost the utilities, not the
// page, so the same structure is styled by hand instead of going unstyled.
// applicationStylesheet is the CSS the project owns. The error pages are always
// in it: their class names come from the framework's own templates rather than
// from a toolchain, so a project that took Tailwind still needs them defined.
func applicationStylesheet(options initOptions) string {
	if options.Tailwind {
		return errorPageStylesheet()
	}
	return landingStylesheet() + errorPageStylesheet()
}

// errorPageStylesheet styles the scaffolded error templates. It is small on
// purpose: an error page is rare, and the one thing it owes a reader is that the
// status and the title are legible.
func errorPageStylesheet() string {
	return `
.error { margin: 0 auto; max-width: 34rem; padding: 4rem 1.5rem; }
.error-status { color: #9aa3b8; font-size: 3rem; font-weight: 700; line-height: 1; margin: 0; }
.error-title { font-size: 1.5rem; margin: .5rem 0 0; }
.error-detail { margin: 1rem 0 0; }
.error-fields { margin: 1rem 0 0; padding-left: 1.25rem; }
.error-fields:empty { display: none; }
.error-code, .error-request { color: #5b6478; font-family: ui-monospace, monospace; font-size: .8rem; margin: 1.5rem 0 0; }
`
}

func landingStylesheet() string {
	return `:root { color-scheme: light dark; --edge: #d8dce4; --muted: #5b6478; --accent: #4f46e5; }
body { font: 16px/1.6 system-ui, sans-serif; margin: 0 auto; max-width: 46rem; padding: 3rem 1.5rem; }
.page > * + * { margin-top: 2.5rem; }
.eyebrow { color: var(--accent); font-size: .8rem; font-weight: 600; letter-spacing: .12em; text-transform: uppercase; }
.title { font-size: 2.25rem; letter-spacing: -.02em; margin: .25rem 0 0; }
.lead { color: var(--muted); margin: .75rem 0 0; }
.card { border: 1px solid var(--edge); border-radius: .75rem; padding: 1.5rem; }
.card-heading { font-size: 1.05rem; margin: 0; }
.card-list { color: var(--muted); margin: .75rem 0 0; padding-left: 1.25rem; }
.card-list li + li { margin-top: .5rem; }
.link { color: var(--accent); font-weight: 500; }
.note { color: var(--muted); font-size: .875rem; }
button, .button { border: 1px solid var(--edge); border-radius: .5rem; background: none; cursor: pointer; font: inherit; padding: .4rem .9rem; }
@media (prefers-color-scheme: dark) { :root { --edge: #333a4a; --muted: #9aa3b8; --accent: #a5b4fc; } }
`
}

// landingSections builds the body of the starter page. Every line of it comes
// from an answer this project was actually scaffolded with, so a declined
// capability is never advertised as present, and the links are literal because
// a landing page that needs a lookup to render is not a starting point.
func landingSections(options initOptions, style landingStyle, self string) string {
	var body strings.Builder
	fmt.Fprintf(&body, "  <section%s>\n    <h2%s>What this project has</h2>\n    <ul%s>\n",
		style.Section, style.Heading, style.List)
	for _, item := range landingIncluded(options, style) {
		fmt.Fprintf(&body, "      <li>%s</li>\n", item)
	}
	body.WriteString("    </ul>\n  </section>\n")

	fmt.Fprintf(&body, "  <section%s>\n    <h2%s>What to do next</h2>\n    <ul%s>\n",
		style.Section, style.Heading, style.List)
	for _, item := range landingNextSteps(options, self) {
		fmt.Fprintf(&body, "      <li>%s</li>\n", item)
	}
	body.WriteString("    </ul>\n  </section>\n")

	fmt.Fprintf(&body, `  <section%s>
    <h2%s>Documentation</h2>
    <ul%s>
      <li><a%s href="%s">Guides, tutorial, and reference</a></li>
      <li><a%s href="%s">Source and issues</a></li>
    </ul>
  </section>
`, style.Section, style.Heading, style.List, style.Link, documentationURL, style.Link, repositoryURL)
	return body.String()
}

// landingIncluded describes the project as it was scaffolded, one line per
// answer that wrote something.
func landingIncluded(options initOptions, style landingStyle) []string {
	var items []string
	if routerHasRegistered(options.Router) {
		items = append(items, "Routes written in Go on <code>"+muxTypeName(options)+"</code>, registered from <code>"+defaultRegisteredDir+"/</code>")
	}
	if routerHasDiscovered(options.Router) {
		items = append(items, "A page tree in <code>"+defaultDiscoveredDir+"/</code>, where a directory with a page template is a route")
	}
	if options.Database {
		items = append(items, "A "+engineFor(options.Engine).Label+" database, with <code>migrations/</code> and typed SQL in <code>queries/</code>")
	}
	if options.Dynamo {
		items = append(items, "A DynamoDB store, with typed records in <code>"+defaultDynamoDir+"/</code>")
	}
	if servesBrowserLogin(options) {
		// The controls are on the starter handler's page, which is this one
		// unless a page tree took the root from it.
		if _, path := registeredHomeRoute(options); path == "/" {
			items = append(items, "Authentication, with the sign-in controls below served by the framework")
		} else {
			items = append(items, `Authentication, with the sign-in controls on <a`+style.Link+` href="`+path+`">`+path+`</a> served by the framework`)
		}
	}
	if options.Tailwind {
		items = append(items, "Tailwind CSS, compiled from <code>assets/app.css</code>")
	}
	if options.Devbox {
		items = append(items, "A Devbox environment pinning the toolchain and the development services")
	}
	return items
}

// landingNextSteps names the commands that do something to this project now.
func landingNextSteps(options initOptions, self string) []string {
	steps := []string{"Edit this page: <code>" + self + "</code>"}
	if routerHasRegistered(options.Router) {
		steps = append(steps, "Add a route: <code>pw new handler</code>")
	}
	if routerHasDiscovered(options.Router) {
		steps = append(steps, "Add a page: <code>pw new page</code>")
	}
	if options.Database {
		steps = append(steps, "Write a query in <code>queries/</code> and a migration in <code>migrations/</code>; both start out commented")
	}
	if declined := declinedCapabilities(options); len(declined) > 0 {
		steps = append(steps, "Install what this project skipped ("+strings.Join(declined, ", ")+"): <code>pw add &lt;capability&gt;</code>")
	}
	return steps
}

// registeredHomeRoute is the pattern the starter handler registers, and the path
// that reaches it.
//
// Both routers register on one mux, and the standard library panics on a
// duplicate pattern rather than letting one of them win. So a project that took
// both cannot have two starter routes claiming the root, and the page tree is
// the one that keeps it: a directory holding a page template is a route by
// existing, and moving that one means scaffolding the root under a directory
// whose name the reader has to look past. The handler takes a path of its own,
// and the two starter pages link to each other, so the difference between the
// routers is something a browser can show rather than something to read about.
func registeredHomeRoute(options initOptions) (pattern, path string) {
	if effectiveRouter(options.Router) == routerBoth {
		return "GET /hello", "/hello"
	}
	return "GET /{$}", "/"
}

// registeredHomeHeader is the top of the starter handler's page. Alone it is the
// project's landing page; beside a page tree it is the other end of a pair, so
// it says which router put it there and links back to the one that did not.
func registeredHomeHeader(options initOptions, style landingStyle) string {
	if effectiveRouter(options.Router) != routerBoth {
		return `  <header>
    <p` + style.Eyebrow + `>Popcorn Web</p>
    <h1` + style.Title + `>{project}</h1>
    <p` + style.Lead + `>Hello, {name}. This page is yours to delete; nothing in the framework reads it.</p>
  </header>
`
	}
	return `  <header>
    <p` + style.Eyebrow + `>{project}</p>
    <h1` + style.Title + `>Hello, {name}</h1>
    <p` + style.Lead + `>A Go function registered itself for <code>GET /hello</code> in <code>handlers/home_handler.go</code>, and the greeting comes from <code>?name=</code>. The <a` + style.Link + ` href="/">landing page</a> is a directory instead.</p>
  </header>
`
}

// registeredHomeSections is what follows that header. Beside a page tree there
// is nothing to add: the landing sections belong to whichever starter page is at
// the root, and two copies of them is what a reader would have to tell apart.
func registeredHomeSections(options initOptions, style landingStyle) string {
	if effectiveRouter(options.Router) == routerBoth {
		return ""
	}
	return landingSections(options, style, defaultRegisteredDir+"/home.pw.html")
}

// discoveredCounterpartLink is the other half of that pair, on the page tree's
// side. It is a sentence rather than a list item because the paragraph it joins
// is already about where a route comes from.
func discoveredCounterpartLink(options initOptions, style landingStyle) string {
	pattern, path := registeredHomeRoute(options)
	if path == "/" {
		return ""
	}
	return ` A Go function took <a` + style.Link + ` href="` + path + `">` + pattern[len("GET "):] + `</a> instead, from <code>` + defaultRegisteredDir + `/</code>.`
}

// muxTypeName names the router type this project's handlers register on.
func muxTypeName(options initOptions) string {
	if options.TinyGo {
		return "pw.ServeMux"
	}
	return "http.ServeMux"
}

// homeTemplateScaffold renders the starter page. The logout control is a form
// because the endpoint accepts POST only.
func homeTemplateScaffold(options initOptions) string {
	style := landingStyleFor(options)
	if !servesBrowserLogin(options) {
		return `package handlers

export component Home(name: string, project: string): html {
<div` + style.Page + `>
` + registeredHomeHeader(options, style) + registeredHomeSections(options, style) + `</div>
}
`
	}
	return `package handlers

export component Home(name: string, project: string, ` + accountParams + `): html {
<div` + style.Page + `>
` + registeredHomeHeader(options, style) + accountSection(style) + registeredHomeSections(options, style) + `</div>
}
`
}

// accountParams are what a starter page needs in order to show the way in and
// the way out. They are a shared string because both routers scaffold this
// section, and a parameter list that disagreed with the section below it would
// fail generation with a message about counts rather than about the login.
const accountParams = "signedIn: bool, email: string, loginPath: url, logoutPath: url, passkey: bool, providerLogin: bool, bootstrap: bool"

// accountSection is the sign-in and sign-out surface, which the framework
// serves and the starter page only points at. Whichever page is at the root
// carries it: it is the one part of a scaffolded login a project cannot be left
// without, because there is otherwise no way to reach the login at all.
func accountSection(style landingStyle) string {
	return `  <section` + style.Section + `>
    <h2` + style.Heading + `>Account</h2>
    {if signedIn}
      <p>Signed in as {email}</p>
      {if passkey}
        <p><button type="button" id="passkey-register">Add a passkey</button></p>
      {/if}
      <form method="post" action={logoutPath}>
        <button type="submit">Sign out</button>
      </form>
      <!-- Signing out keeps the session at the identity provider and makes the next
           sign-in here ask again, which is what auth.oidc.logout_scope defaults to.
           To offer a sign-out-everywhere control as well, set
           auth.oidc.allow_global_logout_request and add a second form posting to
           the same path with <input type="hidden" name="scope" value="global">. -->
    {else}
      {if providerLogin}
        <p><a` + style.Link + ` href={loginPath}>Sign in</a></p>
      {/if}
      {if passkey}
        <p><button type="button" id="passkey-login">Sign in with a passkey</button></p>
      {/if}
      {if bootstrap}
        <form id="passkey-bootstrap">
          <p` + style.Note + `>First sign-in: use the login ID and one-time secret an administrator issued.</p>
          <input name="login_id" placeholder="login ID">
          <input name="secret" type="password" placeholder="one-time secret">
          <button type="submit">Enroll a passkey</button>
        </form>
      {/if}
    {/if}
    {if passkey}
      <p id="passkey-status"></p>
      <script type="module" src="/public/passkey.js"></script>
    {/if}
  </section>
`
}

// discoveredRootCarriesAccount reports whether the page tree root is the page a
// login has to be reachable from.
//
// It is, when the project took no registered router: there is no handler page
// then, and the account section lives on whichever starter page is at the root.
// Without this a scaffolded login had no way in at all — the tree root said the
// framework served sign-in controls below it, and below it was nothing.
func discoveredRootCarriesAccount(options initOptions) bool {
	return servesBrowserLogin(options) && !routerHasRegistered(options.Router)
}

func discoveredRootParams(options initOptions) string {
	if !discoveredRootCarriesAccount(options) {
		return ""
	}
	return accountParams
}

func discoveredAccountSection(options initOptions, style landingStyle) string {
	if !discoveredRootCarriesAccount(options) {
		return ""
	}
	return accountSection(style)
}

// devConsoleProjectConfig pins the development console port and the corner its
// launcher takes.
//
// The port is the one development listener with a written-down number, and it
// is written down for the opposite reason to dev.idp.port: nothing derives an
// identity from it, but it is the address a developer bookmarks and returns to
// all day, and a reserved port would hand out a new one every run.
//
// The corner is written down for a different reason again. Both keys work
// perfectly well absent — the launcher is on by default and takes the bottom
// left — but that is a default a developer meets by finding a button sitting
// over their own layout, and an empty section tells them nothing about how to
// move it. This is the one console setting the project itself has an opinion
// about, so the scaffold states it where the answer is wanted.
func devConsoleProjectConfig() string {
	return `
# The pw dev console: the telemetry viewer, the asset report, and whatever else
# the loop can say about the project, on one loopback address.
[dev.console]
port = ` + strconv.Itoa(defaultConsolePort) + `

# The floating link to that console, in a corner of every page pw dev serves.
# Move it when your own layout wants this corner: ` + strings.Join(launcherCorners, ", ") + `.
# Set enabled = false to serve no launcher at all.
[dev.console.launcher]
corner = "` + defaultLauncherCorner + `"
`
}

// devIdPProjectConfig enables the development identity provider for pw dev.
func devIdPProjectConfig(options initOptions) string {
	if !options.AuthEmulator || !usesOIDC(options.Auth) {
		return ""
	}
	return `
[dev.idp]
enabled = true
config = "` + defaultIdPConfig + `"
# A fixed port keeps the issuer URL stable across restarts, and the issuer is
# half of what identifies an account: the scaffolded resolver builds an account
# ID out of the issuer and the subject. On an automatically reserved port every
# pw dev would hand the same person a new account, and everything stored against
# the old one would be gone. Change it if something else here already listens.
port = ` + strconv.Itoa(defaultIdPPort) + `
`
}

// devIdPRoster is the starter user list. Every value here is a development
// fixture: the provider checks no credential, so nothing in it is a secret.
func devIdPRoster() string {
	return `# Development identity provider users, selected on the login screen.
# pw dev serves these; no password is checked, so this file never ships.

[users.admin]
display_name = "Administrator"
extra_scopes = ["admin"]
[users.admin.claims]
email = "admin@example.com"
role = "admin"

[users.member]
display_name = "Member"
[users.member.claims]
email = "member@example.com"
role = "member"
`
}

// authRuntimeConfig writes the [auth] section for the selected mode. The OIDC
// provider values stay empty for the emulator because pw dev injects them, and
// the application refuses to start if neither the file nor the environment
// supplies them.
// sessionRuntimeConfig writes the [session] section.
//
// It is written whether or not the project serves a login, because session
// storage is not a login: a project with a language preference or a dismissed
// notice declares a slot and needs the middleware, and the framework installs
// it from session.enabled rather than from an authentication plugin.
//
// Without a login the record is bounded by session.retention alone, since the
// [auth] durations that would otherwise narrow it are not there.
func sessionRuntimeConfig(options initOptions) string {
	// The browser token is opaque in every backend; this selects where the
	// record behind it lives. General server backends reach the binary through
	// the blank import in main; cookie and development modes are built in.
	backend := developmentSessionBackend(options)
	keyring := ""
	if backend != sessionDevVolatile {
		keyring = `# One secret signs and seals everything the browser carries, whatever the
# backend is: a session.ReadOnly slot is signed and a session.Private slot is
# sealed, including while a visitor is still anonymous.
#
# This value was generated for this project and belongs to development only.
# Every other environment reads SESSION_KEYRING_SECRET, and "pw doctor --env"
# reports a literal here as an error for any environment but dev.
keyring.secret = "` + generatedKeyringSecret() + `"
`
	}
	return `
[session]
enabled = true
backend = "` + backend + `"
cookie.name = "pw_session"
# Loopback development only, because there is no TLS to require here. Every
# other environment refuses to start with this false, so a deployment file
# either sets it true or leaves it out.
cookie.secure = false
` + keyring + sessionBackendConfig(backend) + `
`
}

// authRuntimeConfig writes the [auth] section for the selected mode. The OIDC
// provider values stay empty for the emulator because pw dev injects them, and
// the application refuses to start if neither the file nor the environment
// supplies them.
func authRuntimeConfig(options initOptions) string {
	if !servesLogin(options) {
		return ""
	}
	if options.Auth == authJWTOnly {
		// A resource server mounts no login, sets no cookie, and starts no
		// ceremony, so none of the session lifetimes or the protection
		// redirect below applies to it. The backend key is left out with them:
		// it names the storage of four ceremony stores this mode never opens.
		return `
# This application verifies a bearer token somebody else issued. It mounts no
# login, no callback, and no logout: those are redirects a machine client
# cannot follow.
[auth]
enabled = true
mode = "` + authConfigMode(options.Auth) + `"
# Opt in per path; everything else stays public. An unauthenticated request to
# an included path is answered 401, because there is nowhere to redirect a
# client that authenticates with its own issuer.
protection.include = []
protection.unauthenticated = "unauthorized"
` + authJWTConfig(options) + authJWTDevelopmentConfig()
		// The relaxation is appended here rather than guarded, because this
		// section is written into config.dev.toml and nothing else. A stg or
		// prod file added later carries the section above and not this line.
	}
	section := `
# The framework serves every authentication path itself, so the application
# registers no authentication route. Logout is POST only.
[auth]
enabled = true
# The four tables plugin/auth owns move together, so one key names their store.
backend = "` + authBackend(options) + `"
mode = "` + authConfigMode(options.Auth) + `"
post_login_path = "/"
# Every session lifetime is declared here rather than under [session]: an
# expiry states how long a proof of identity stays good, and the store holding
# the bytes has no basis to make that statement.
session.ttl = "12h"
session.idle_timeout = "1h"
# Opt in per path; everything else stays public.
protection.include = []
protection.unauthenticated = "redirect"
`
	if usesPasskey(options.Auth) {
		section += authPasskeyConfig(options)
	}
	if usesOIDC(options.Auth) {
		section += authOIDCConfig(options)
	}
	return section
}

// securityRuntimeConfig writes the [security] section.
//
// CSRF stays off until the paths it covers are named — a check installed over
// nothing reads as protection that is not there — so the section is written for
// the projects that name some on the way in.
//
// A login names one: the sign-out control is a form, because the endpoint
// accepts POST only. A page tree names one too, since a template declaring a
// form action registers POST on the page's own path, and that form carries a
// token whether or not the project ever wired a check to verify it.
//
// The earlier gate was the login alone, on the ground that a project with no
// session has nothing to bind a token to. That was already untrue when it was
// written: sessionRuntimeConfig writes session.enabled unconditionally, for the
// reason it gives, that session storage is not a login. So a page-tree project
// with no login was scaffolded able to hold a secret and configured not to,
// which is a form posting unverified rather than a check that could not pass.
func securityRuntimeConfig(options initOptions) string {
	if !servesBrowserLogin(options) && !hasDiscoveredPages(options) {
		// Neither an unsafe form on the way in nor a route tree that can grow
		// one. Naming paths for a check nothing crosses is what this declines —
		// but an API this preset scaffolds is the one project whose caller is
		// likely to be a page served from somewhere else, so the cross-origin
		// block goes out even though the CSRF one cannot.
		return corsRuntimeConfig(options)
	}
	// Why the check is on names the unsafe route this project already ships,
	// because a reader deciding whether to keep it needs the one they have
	// rather than the general case.
	why := `# A form declaring a server action carries a token from here, and turning this
# off serves that form with an empty one rather than refusing it. Keep the
# include list as narrow as the routes that mutate.
`
	if servesBrowserLogin(options) {
		why = `# This login ships an unsafe route already: the sign-out control posts. The
# token that form carries comes from here, so this stays on for as long as the
# login does. Keep the include list as narrow as the routes that mutate.
`
	}
	section := `
# Partial updates are off until a project wants a page to refresh a region
# rather than reload. The validator key is required with them: an unkeyed digest
# of low-entropy content lets a guess be confirmed by comparing digests, so
# startup refuses the combination rather than serving one.
[html.update]
enabled = false
# validator_key = "${HTML_UPDATE_VALIDATOR_KEY}"

` + why + `[security]
csrf.enabled = true
csrf.include = ["/**"]
`
	if hasDiscoveredPages(options) {
		// A page action is a POST reachable with ambient credentials, and
		// nothing else stands in front of it, so it is the one prefix a page
		// tree must not leave out.
		section += `# Page actions are POST endpoints reachable with the session cookie, so the
# action prefix belongs in the include list of any page tree.
csrf.include = ["/_action/**", "/**"]
`
	}
	section += `# Exclude what a browser never posts: a webhook has no session and carries its
# own authentication.
csrf.exclude = []
# A public page with its own unsafe form needs a token before there is a
# session. This issues one in a signed cookie and writes no session record.
# csrf.anonymous.enabled = true
# csrf.anonymous.secret = "${SECURITY_CSRF_ANONYMOUS_SECRET}"
`
	return section
}

// corsRuntimeConfig writes the commented cross-origin block, for an API project
// only.
//
// It is commented rather than configured off, because the two values a
// deployment has to supply — which origins, and which methods beyond the three
// a browser sends without asking — are the whole content of the decision, and a
// key set to a default nobody chose reads as one somebody did. Uncommenting it
// with an origin filled in is the entire setup.
//
// Nothing here mentions the OpenAPI document: it answers a wildcard origin on
// its own, whatever this section says, so a documentation UI hosted elsewhere
// already works.
func corsRuntimeConfig(options initOptions) string {
	if !servesAPI(options) {
		return ""
	}
	return `
# Which other origins may read this API from a browser.
#
# A page served from somewhere else cannot read this application's responses
# without it, whatever status they carry: the browser withholds the body and the
# status alike, so a 401 and a 429 arrive as the same network error. Turning
# this on is what makes the typed answers this API already writes readable.
#
# Nothing is needed for a caller that is not a browser, and nothing is needed
# for the generated OpenAPI document, which is readable from anywhere already.
# [security.cors]
# enabled = true
# allowed_origins = ["https://app.example.com"]
# Beyond GET, HEAD and POST, which a browser sends without asking first.
# allowed_methods = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"]
# A bearer API wants no cookies, so this stays off. Turning it on grants the
# origins above a read of this deployment as the logged-in visitor, and it
# refuses an include of "/**" for exactly that reason.
# allow_credentials = false
`
}

// apiCatalogRuntimeConfig writes the RFC 9727 switch into every scaffolded
// configuration, so the endpoint is learned from the file that already
// configures the three endpoints it links to rather than from the reference.
//
// The value differs by preset. The machine-facing one set out to publish the
// API this announces, so it ships on. Everywhere else it ships off: an
// application that serves pages publishes an API as a side effect of having
// handlers, which is not the same as deciding to publish one, and a key written
// off is still a key the reader has read.
func apiCatalogRuntimeConfig(options initOptions) string {
	if servesAPI(options) {
		return `# RFC 9727: /.well-known/api-catalog answers with a Linkset pointing at the
# three keys above, so a client handed nothing but this origin finds the
# document, the reference UI, and the probe.
#
# The links are written relative unless api_catalog_origin names this
# deployment's origin. Relative resolves correctly for anything following the
# catalog; name the origin once something stores one instead.
api_catalog = true
# api_catalog_origin = "https://api.example.com"
`
	}
	return `# RFC 9727: turning this on answers /.well-known/api-catalog with a Linkset
# pointing at the three keys above, so a client handed nothing but this origin
# finds the document, the reference UI, and the probe. It stays off until an API
# is something this project publishes on purpose rather than as a side effect of
# having handlers.
#
# The links it would publish are relative unless api_catalog_origin names this
# deployment's origin, which matters only to a reader that stores the catalog
# rather than following it.
api_catalog = false
# api_catalog_origin = "https://api.example.com"
`
}

// servesAPI reports whether the project is the machine-facing preset, whose
// caller may well be a browser page on another origin.
func servesAPI(options initOptions) bool {
	return options.Auth == authJWTOnly
}

// hasDiscoveredPages reports whether the project starts with a page tree, whose
// action endpoints are what the CSRF include list must cover.
func hasDiscoveredPages(options initOptions) bool {
	return len(scaffoldGenerationScope(options).Pages) > 0
}

// authConfigMode maps the scaffold choice onto the plugin/auth mode name.
func authConfigMode(mode string) string {
	switch mode {
	case authOIDCPasskey:
		return "oidc_passkey"
	case authPasskey:
		return "passkey_only"
	case authJWTOnly:
		return "jwt_only"
	default:
		return "oidc_only"
	}
}

// authJWTConfig writes the resource-server section.
//
// The issuer and the audience are left empty, and startup fails naming
// whichever is missing. That is the same shape as the external-provider answer
// of the OIDC question, and it is the point: this application verifies tokens
// an authorization server somebody else runs has minted, and no scaffold can
// know which one or what it registered this API as. A written-out example
// would be a value that parses, and a value that parses is a value somebody
// ships.
//
// Everything else is stated rather than defaulted, because the mode refuses to
// start on an omission: a permissive answer here would be the silent one.
func authJWTConfig(options initOptions) string {
	return `
# The authorization server whose tokens this API accepts.
#
# These two are development placeholders and neither is contacted here: under
# "pw dev" the token is read without being verified, so nothing fetches this
# issuer's keys. They exist because the mode refuses to start without them,
# which is the right rule everywhere and would otherwise leave this project
# unable to run at all before you have an authorization server.
#
# Replace them in every deployed environment, from the environment:
# AUTH_JWT_ISSUER, AUTH_JWT_AUDIENCE. There is no default for either — a
# missing one fails startup naming the field rather than verifying nothing.
[auth.jwt]
issuer = "` + authDevelopmentIssuer + `"
audience = ["` + options.Name + `"]
# The development issuer above is loopback http, which an https-only client
# refuses. A real issuer is https and this goes back to false.
allow_loopback_http = true
# Which signatures this API accepts. The token header names an algorithm and is
# not trusted to choose one, so the list is stated here. Only RSA is offered:
# the verification key comes from a published JWKS, and accepting an HMAC
# algorithm would let anyone holding that public document mint their own tokens.
algorithms = ["RS256"]
# Which verified identities may use this API. "authenticated" admits everyone
# the issuer above verified, which is right when that issuer only mints tokens
# for people already entitled to this API. A shared issuer wants "claim"
# instead, with claim.path and claim.values naming the tenant, department, or
# application; "registered" admits an allowlist, and takes a database with it.
admission = "authenticated"
# Which claim is the stable identity. Read auth.User in a handler only after
# installing a resolver; without one the request still carries the verified
# subject and claims.
identity_claim = "sub"
# Bounds the exp-minus-iat check. This application cannot know how long the
# issuer mints for, so it states what it is willing to accept.
max_token_lifetime = "1h"
# No revocation list, so a verified token is good until it expires. Turning
# this on takes a database and the popcornweb_revoked_token migration with it.
revocation.mode = "off"
`
}

// authJWTDevelopmentConfig relaxes verification for the development file only.
//
// It is what makes a scaffolded API server developable on the first command:
// there is no authorization server yet, and this admits a hand-written token
// so there is something to curl. Every lock stays on — the pwdev build, a
// development environment, this field, and a loopback address, all four at
// once — and no other environment file carries the field at all. "pw doctor"
// reports it as an error wherever else it appears.
func authJWTDevelopmentConfig() string {
	return `
# Development only, and only under "pw dev" from this machine. A token is read
# without checking its signature, its issuer, its audience, or its expiry, so
# any hand-written JWT signs you in:
#
#   b64() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
#   head=$(printf '{"alg":"none","typ":"at+jwt"}' | b64)
#   body=$(printf '{"iss":"` + authDevelopmentIssuer + `","sub":"dev-user"}' | b64)
#   curl -H "Authorization: Bearer $head.$body." http://127.0.0.1:8080/me
#
# The third segment is empty, because there is no signature to put there.
#
# "iss" and "sub" both have to be present. Nothing checks what the issuer says,
# but the pair identifies the caller: the account this API sees is derived from
# them, so changing either is how you develop as somebody else.
#
# Admission and revocation still run, so what you develop against is the real
# rule rather than a bypass of it. A build without the development tag refuses
# to start on this field rather than ignoring it.
dev.trust_unverified_tokens = true
`
}

// authPasskeyConfig writes the relying-party registration and the account
// lifecycle policies the selected mode requires.
func authPasskeyConfig(options initOptions) string {
	lifecycle := `
# Two login methods, so a lost passkey is recoverable through the provider.
recovery.policy = "oidc"
`
	if options.Auth == authPasskey {
		// Nothing can stand in for a provider, so both policies are explicit
		// and the issued credential is bounded.
		lifecycle = `
# No provider can stand in for either policy, so both are chosen here. An
# administrator issues the login ID and one-time secret that open a first
# enrollment; see handlers/accounts.go.
registration.policy = "administrator"
recovery.policy = "administrator"
# How long an issued secret stays redeemable, measured from issuance: it spans
# delivery, so it is the longer of the two.
bootstrap.issue_ttl = "24h"
# How long the enrollment stays open after a successful redemption: it spans one
# ceremony at the keyboard, so it is short. A redemption grants a ticket for one
# registration, not a session, so the request stays unauthenticated until the
# registration finishes.
bootstrap.enrollment_ttl = "10m"
bootstrap.max_attempts = 5
`
	}
	return lifecycle + `# How recently a request must have authenticated before it may add or remove a
# login method.
recent_auth_max_age = "5m"

# A relying party is scoped to a domain. "localhost" is a secure origin for
# WebAuthn without TLS, which is why development needs no certificate; an IP
# literal such as 127.0.0.1 can never be an RP ID.
[auth.passkey]
rp_id = "localhost"
rp_name = "` + options.Name + `"
origins = ["http://localhost:8080"]
user_verification = "required"
discoverable = "preferred"
`
}

// authOIDCConfig writes the provider registration. The values stay empty for
// the emulator because pw dev injects them, and the application refuses to
// start if neither the file nor the environment supplies them.
func authOIDCConfig(options initOptions) string {
	provider := `
# Supply these from the environment in every deployed environment:
# AUTH_OIDC_ISSUER, AUTH_OIDC_CLIENT_ID, AUTH_OIDC_CLIENT_SECRET.
issuer = ""
client_id = ""
client_secret = ""`
	loopback := "false"
	redirect := `redirect_url = "` + authDevelopmentOrigin(options) + `/auth/callback"`
	if options.AuthEmulator {
		provider = `
# pw dev runs the development identity provider and injects AUTH_OIDC_ISSUER,
# AUTH_OIDC_CLIENT_ID, and AUTH_OIDC_CLIENT_SECRET, so these stay empty here.
# Running the application without pw dev requires setting them yourself.`
		// The development issuer is loopback http, which an https-only client
		// would refuse.
		loopback = "true"
		redirect = `# redirect_url is derived from the loopback request Host.`
	}
	return `
[auth.oidc]` + provider + `
` + redirect + `
scopes = ["profile", "email"]
identity_claim = "sub"
admission = "authenticated"
auto_provision = true
# What a logout does to the provider session, which is shared with every other
# application signed in through it.
#   reconfirm: keep it, and make the next login here ask again
#   global:    end it, signing the user out of those applications too
logout_scope = "reconfirm"
allow_loopback_http = ` + loopback + `
`
}

// authDevelopmentOrigin is the origin the browser uses in development.
//
// It is the name rather than the address for every mode, and the reason is the
// same one that makes it mandatory under a passkey: an origin is not an
// address. A login begun at one origin and returning to another returns to a
// different cookie jar, so the state the callback verifies is a cookie the
// browser does not send — the redirect arrives and is refused, having proved
// nothing. localhost is what pw dev prints and what a developer therefore
// opens, so it is the one this has to be.
//
// Both names reach the same machine, and RFC 6761 makes localhost loopback by
// reservation rather than by resolution, so the loopback allowance covers it.
func authDevelopmentOrigin(options initOptions) string {
	return "http://localhost:8080"
}

// sessionBackendConfig writes the keys of the selected backend and only those:
// keys of a backend nothing opens would describe storage that is not there.
func sessionBackendConfig(backend string) string {
	switch backend {
	case sessionDevVolatile:
		return `# Development records stay in this process and disappear on restart.
# Private slots are server-side from their first write, so no sealed record
# cookie or development keyring is needed unless a ReadOnly slot is declared.
`
	case sessionDevPersist:
		return `# Development records stay sealed in a browser cookie and survive
# process restarts. The stable keyring above keeps issued records readable.
`
	case sessionCookie:
		return `# The record stays sealed in a second cookie for the whole of a session, so
# this deployment stores no sessions at all. It uses the same keyring.secret
# above; during a rotation the old value moves into keyring.previous_secrets,
# which keeps issued records readable.
`
	case sessionRedis:
		return `# Redis or Valkey holds each record under its own TTL, so no sweep runs.
redis.dsn = "redis://127.0.0.1:6379/0"
redis.key_prefix = "pw:session:"
`
	default:
		return `# The session table lives in the database configured above, created by the
# scaffolded migration.
rdb.source = "middleware"
`
	}
}

// projectToolchain names the compiler the project is scaffolded for.
func projectToolchain(options initOptions) string {
	if options.TinyGo {
		return toolchainTinyGo
	}
	return toolchainGo
}

// muxScaffold emits the route registry. TinyGo projects go through pw.ServeMux
// so one import works on both toolchains; host-only projects keep the standard
// library type, which api:cli-generate discovers just the same.
// mainScaffold writes the entry point for the routers the project took. Both
// register on one mux, and the order they do it in does not matter: a generated
// page route does not shadow a hand-registered subtree, and the standard
// library panics on a duplicate rather than silently letting one win.
func mainScaffold(options initOptions) string {
	name := options.Name
	registered, discovered := defaultRegisteredDir, defaultDiscoveredDir
	registeredPkg, discoveredPkg := goPackageIdentifier(registered), goPackageIdentifier(discovered)
	imports := "\t\"context\"\n\t\"log\"\n\n"
	body := ""
	handler := ""
	switch router := effectiveRouter(options.Router); {
	case router == routerRegistered:
		imports += "\t\"" + name + "/" + registered + "\"\n\t\"github.com/shibukawa/popcornweb/pw\""
		handler = registeredPkg + ".Handlers()"
	case router == routerDiscovered:
		imports += "\t\"" + name + "/" + discovered + "\""
		if servesBrowserLogin(options) {
			// The account seams live in the handler package whichever router
			// this project took, and authBootstrap calls them below. A page
			// tree with a login therefore imports a directory it serves no
			// route from.
			imports += "\n\t\"" + name + "/" + registered + "\""
		}
		imports += "\n\t\"github.com/shibukawa/popcornweb/pw\""
		body = "\tmux := pw.NewServeMux()\n\t" + discoveredPkg + ".Register(mux)\n"
		handler = "mux"
	default:
		imports += "\t\"" + name + "/" + registered + "\"\n\t\"" + name + "/" + discovered +
			"\"\n\t\"github.com/shibukawa/popcornweb/pw\""
		body = "\t// The page routes join the handler mux. Registration order does not\n" +
			"\t// matter; a duplicate pattern would panic here rather than shadow.\n" +
			"\tmux := " + registeredPkg + ".Handlers()\n\t" + discoveredPkg + ".Register(mux)\n"
		handler = "mux"
	}
	return `package main

import (
` + imports + databaseDriverImport(options) + storeMiddlewareImport(options) + sessionBackendImport(options) + `
)

func main() {
	// Names the API document served at server.openapi_path and shown by the
	// reference UI at /docs. Without it both fall back to "Application API".
	if err := pw.SetOpenAPIInfo(pw.OpenAPIInfo{Title: "` + name + `", Version: "0.1.0"}); err != nil {
		log.Fatal(err)
	}
` + authBootstrap(options) + `
` + body + `	if err := pw.Run(context.Background(), ` + handler + `); err != nil {
		log.Fatal(err)
	}
}
`
}

// registeredRouterScaffold writes a handler package into directory: the mux the
// package owns, one route, and the page it renders.
//
// The directory is a parameter because it is a data:project-config value: the
// scaffold picks the default, and everything downstream reads generate.handlers
// rather than the name.
func registeredRouterScaffold(options initOptions, directory string) map[string]string {
	files := map[string]string{directory + "/index.go": muxScaffold(options)}
	if options.Auth == authJWTOnly {
		// A resource server answers machine clients. Its starter route shows
		// reading the verified identity, which is what this project is for,
		// and the landing page is left out rather than written for a browser
		// none of its callers is using.
		//
		// The document shell and the error templates in templates/ stay: the
		// error renderer still answers a browser that reaches a failing route,
		// and requirement:typed-http-contract answers everything else.
		files[directory+"/me_handler.go"] = bearerHandlerScaffold(options)
		files[directory+"/me_types.go"] = identityScaffold()
		return files
	}
	files[directory+"/home_handler.go"] = homeHandlerScaffold(options)
	if !servesBrowserLogin(options) {
		// The login variant reads the session rather than a query, so it
		// declares no request type to move out.
		files[directory+"/home_types.go"] = homeInputScaffold()
	}
	files[directory+"/home.pw.html"] = homeTemplateScaffold(options)
	return files
}

// bearerHandlerScaffold writes the endpoint that reports who the bearer token
// says the caller is.
//
// It answers a typed value rather than a page: every client of this
// application arrives with an Authorization header, and none of them follows a
// redirect or renders HTML. What it demonstrates is that the identity is
// already on the context by the time a handler runs — the middleware verified
// the token, applied the admission rule, and either refused the request or put
// this here.
func bearerHandlerScaffold(options initOptions) string {
	return firstBuild(options) + `package handlers

import (
	"errors"
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
)

func init() { mux.HandleFunc("GET /me", me) }

// me reports the verified identity of the caller.
//
// There is no token parsing here and no header to read. By the time this runs
// the framework has verified the signature against the issuer's key set,
// checked the audience, the expiry, and the token type, applied the admission
// rule of auth.jwt.admission, and consulted the revocation list when one is
// configured. A request that failed any of those never reached this function.
func me(w http.ResponseWriter, r *http.Request) {
	// The zero value when nothing authenticated the request, which is what a
	// path outside auth.protection.include gets. Adding "/me" to that list
	// makes the framework answer 401 before this handler runs, and this check
	// is what a path left public still needs.
	authentication := pw.RequestAuthentication(r)
	if !authentication.Authenticated {
		pw.WriteProblem(w, r, pw.Unauthorized(errors.New("this route needs a bearer token")))
		return
	}
	pw.WriteAPI(w, r, identity{
		Subject: authentication.Subject,
		// "bearer" here, because that is what verified this request.
		Method: authentication.Method,
		Scope:  authentication.Scope,
	})
}
`
}

// pageTreeScaffold writes a page tree into root: a layout, the root page, and
// one dynamic route whose Go entry point runs between the request and the
// render.
//
// The root is a parameter for the same reason the handler directory is: it is
// what generate.pages names, and a project may name it something else.
func pageTreeScaffold(options initOptions, root string) map[string]string {
	// The tree root is a Go package like every directory below it, so its name
	// follows the directory rather than the other way round.
	pkg := goPackageIdentifier(root)
	style := landingStyleFor(options)
	files := map[string]string{
		// A layout must declare children as html: that shape is what makes the
		// template compiler emit the wrapper the generated chain calls.
		root + "/" + pwgen.LayoutFile: `package ` + pkg + `

export component Layout(children: html): html {
  <div class="page"><slot required /></div>
}
`,
		// A directory holding this file is a route. This one is the tree root,
		// which serves GET /.
		root + "/" + pwgen.PageFile: `package ` + pkg + `

export component Page(` + discoveredRootParams(options) + `): html {
<div` + style.Page + `>
  <header>
    <p` + style.Eyebrow + `>Popcorn Web</p>
    <h1` + style.Title + `>` + options.Name + `</h1>
    <p` + style.Lead + `>A directory holding a page template is a route, so <a` + style.Link + ` href="/greet/world">/greet/world</a> is served by the directory beside this one.` + discoveredCounterpartLink(options, style) + `</p>
  </header>
` + discoveredAccountSection(options, style) + landingSections(options, style, root+"/"+pwgen.PageFile) + `</div>
}
`,
		// One trailing underscore marks a dynamic segment, so this directory
		// serves GET /greet/{name}. Brackets are impossible: the directory is
		// also a Go package, and the toolchain rejects an illegal import path
		// element before it evaluates anything else.
		root + "/greet/name_/" + pwgen.PageFile: `package name_

external LoadGreeting(name: string): string

export component Page(name: string): html {
  {val greeting = LoadGreeting(name)}
  <h1 class="text-3xl font-bold">{greeting}</h1>
}
`,
		// page.go is optional. Adding it gives the page Go of its own to load
		// with: the template declares what it needs and binds the result once,
		// and this implements it. The component's own parameters are the route's
		// dynamic segments in order, then its query parameters.
		root + "/greet/name_/page.go": `package name_

// Declare a leading context.Context to receive the request's, which is where
// the database handle and the signed-in reader live.
func LoadGreeting(name string) (string, error) {
	return "Hello, " + name, nil
}
`,
	}
	if discoveredRootCarriesAccount(options) {
		files[root+"/page.go"] = discoveredRootLoadScaffold(options, pkg)
	}
	return files
}

// discoveredRootLoadScaffold puts the tree root on the handler rung, which is
// the rung a page takes when it needs the request itself.
//
// The session is on the request context, and the rung below this one receives
// the route's inputs rather than the request — a typed Load is handed path and
// query values, and an external the template calls is handed its arguments. So
// a page that reads who is signed in is a handler-rung page, and this is the
// smallest one that does.
//
// It composes its own chain for a reason that is structural rather than a
// missing convenience: a leaf imports the root for its ancestor layouts, so the
// root cannot import a composer from below it, and the generated registry is
// where composition lives. A handwritten Load therefore assembles the wrappers
// the registry would have assembled, which for the root is its own layout.
func discoveredRootLoadScaffold(options initOptions, pkg string) string {
	return firstBuild(options) + `package ` + pkg + `

import (
	"net/http"
	"net/url"

	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwpage"
)

// Load renders the starter landing page, signed in or not.
//
// A page whose data comes from a loader the template binds is generated whole.
// This one takes the request instead, because it decides the whole response:
// what it renders depends on whether there is a session, and a signed-out
// visitor is answered differently rather than with different values.
func Load(w http.ResponseWriter, r *http.Request) {
	// The framework resolved the session before this handler ran.
	user, signedIn := auth.User(r.Context())
	params := PageParams{
		SignedIn:      signedIn,
		Email:         user.Email,
		LoginPath:     url.URL{Path: "/auth/login"},
		LogoutPath:    url.URL{Path: "/auth/logout"},
		Passkey:       ` + passkeyLiteral(usesPasskey(options.Auth)) + `,
		ProviderLogin: ` + passkeyLiteral(usesOIDC(options.Auth)) + `,
		Bootstrap:     ` + passkeyLiteral(options.Auth == authPasskey) + `,
	}
	// The ancestor layouts of this route, outermost first. The root has one.
	wrappers := []pwpage.Wrapper{BindLayout(LayoutParams{})}
	if err := pwpage.Render(w, r, wrappers, Page(params)); err != nil {
		pw.WriteProblem(w, r, err)
	}
}
`
}

func muxScaffold(options initOptions) string {
	if options.TinyGo {
		return firstBuild(options) + `package handlers

import "github.com/shibukawa/popcornweb/pw"

var mux = pw.NewServeMux()

func Handlers() *pw.ServeMux { return mux }
`
	}
	return firstBuild(options) + `package handlers

import "net/http"

var mux = http.NewServeMux()

func Handlers() *http.ServeMux { return mux }
`
}

// scaffoldGenerationScope maps the starter directories onto the purposes that
// read them. The scaffold writes every purpose explicitly because none has a
// default, so what each purpose reads is readable from the first run. handlers
// appears twice because the starter page template sits beside its handler, and
// the main package carries the configuration the application registers.
func scaffoldGenerationScope(options initOptions) generationScope {
	scope := generationScope{
		// templates always holds the document shell and the error pages, which
		// both routers render through.
		Templates: []string{defaultTemplatesDir},
		Config:    []string{"cmd/" + options.Name},
	}
	if routerHasRegistered(options.Router) {
		scope.Handlers = []string{defaultRegisteredDir}
		// A page template sits beside the handler that renders it.
		scope.Templates = []string{defaultRegisteredDir, defaultTemplatesDir}
	}
	if routerHasDiscovered(options.Router) {
		// A tree root is one generation run, not a directory of independent
		// sources, so it is never listed under another purpose.
		scope.Pages = []string{defaultDiscoveredDir}
	}
	if options.Database {
		// A purpose may only name directories that exist, so a project without
		// the database says so with an empty list rather than a stale entry.
		scope.Queries = []string{"queries"}
	}
	if options.Dynamo {
		scope.Dynamo = []string{defaultDynamoDir}
	}
	if options.Firestore {
		scope.Firestore = []string{defaultFirestoreDir}
	}
	return scope
}

// quotedList renders a string slice as the body of a TOML or JSON array.
func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = strconv.Quote(value)
	}
	return strings.Join(quoted, ", ")
}

// presenceBrowserScaffold writes the browser half of auth.assurance.presence.
//
// Include it from a page only when that setting is on. It reports one bit per
// tick, so nothing about what the user did leaves the browser and behavioral
// analysis is impossible rather than merely discouraged.
func presenceBrowserScaffold() string {
	return `// Reports whether anybody is at the keyboard, so a session ends when a person
// leaves rather than when requests stop arriving. Those are different things: a
// page holding a live connection keeps requesting with nobody there, and a
// person reading one page for an hour requests nothing at all.
//
// What is sent is one boolean per tick and, when the clock jumped, how far.
// No key, no coordinate, and no timing pattern leaves this file.

const INTERVAL_MS = 60_000;

// Set by any input and cleared by each report, so the whole of the state is
// "did anything happen since last time".
let active = false;
let lastTick = Date.now();

const mark = () => { active = true; };
for (const type of ["pointerdown", "pointermove", "keydown", "wheel", "scroll", "touchstart"]) {
  // Passive, and it returns immediately once the flag is set, so even
  // pointermove costs nothing measurable.
  addEventListener(type, mark, { passive: true });
}
// A tab becoming visible is interaction; becoming hidden is not, and is left to
// the tick to notice.
addEventListener("visibilitychange", () => { if (!document.hidden) mark(); });

async function tick() {
  const now = Date.now();
  // Nothing reports a machine waking. A gap far larger than the interval is
  // how it is inferred, and it counts as absence rather than as presence.
  const gap = Math.max(0, Math.round((now - lastTick - INTERVAL_MS) / 1000));
  lastTick = now;
  const report = { active, gap };
  active = false;
  try {
    const response = await fetch("/auth/logout/presence", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(report),
      credentials: "same-origin",
    });
    // The server ends the session when it decides nobody is here. Reloading
    // lands on whatever an anonymous visitor sees.
    if (response.status === 401 || response.status === 403) location.reload();
  } catch {
    // A failed report is not a presence claim. The server treats silence as
    // absence, which is the safe direction.
  }
}

setInterval(tick, INTERVAL_MS);
`
}

// editorConfigScaffold states the indent, encoding, and line endings the
// scaffolded sources are written with. The Go rule restates what gofmt already
// does, so an editor with no Go support does not fight it, and the two-space
// rule is the width a .pw.html block is indented by.
func editorConfigScaffold() string {
	return `root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
trim_trailing_whitespace = true

[*.go]
indent_style = tab

[*.{pw.html,html,pw.sql,sql,toml,json,css,js,mjs,yaml,yml}]
indent_style = space
indent_size = 2
`
}

// editorExtensionsScaffold recommends the extensions the scaffolded sources
// need to be edited the way they were written. Recommendations only: nothing is
// listed as unwanted, and nothing here is required to build the project.
func editorExtensionsScaffold(options initOptions) string {
	recommendations := []string{"golang.go", "EditorConfig.EditorConfig"}
	// A declined capability is never advertised as present, so a project
	// without Tailwind is not told to install its extension.
	if options.Tailwind {
		recommendations = append(recommendations, "bradlc.vscode-tailwindcss")
	}
	var body strings.Builder
	body.WriteString("{\n    \"recommendations\": [\n")
	for index, name := range recommendations {
		separator := ","
		if index == len(recommendations)-1 {
			separator = ""
		}
		fmt.Fprintf(&body, "        %q%s\n", name, separator)
	}
	body.WriteString("    ]\n}\n")
	return body.String()
}

// errorRegistrationScaffold connects the error templates to the framework.
// Without it they are generated and never reached, which is what they were
// before this file existed.
func errorRegistrationScaffold() string {
	return `package templates

// The shared leaf rather than a runtime: this file belongs to both builds, and
// the error page registry is one process-wide table either of them reads.
import "github.com/shibukawa/popcornweb/pwruntime"

// The framework renders one of these when a request fails and the client would
// rather have a page than a problem document. It also renders one in place of a
// page whose async boundary failed with no recover clause.
//
// The problem arrives already bounded: outside development it carries the
// status and the title only, so nothing here has to decide what is safe to
// show. Add a status to the switch and the framework starts using it.
func init() {
	pwruntime.RegisterHTMLErrorPage(func(problem pwruntime.Problem) pwruntime.HTMLFragment {
		fields := make([]string, 0, len(problem.Fields))
		for _, field := range problem.Fields {
			fields = append(fields, field.Field+": "+field.Message)
		}
		switch problem.Status {
		case 400:
			return Error400(Error400Params{Status: problem.Status, Title: problem.Title,
				Detail: problem.Message, Code: problem.Code, Fields: fields})
		case 401:
			return Error401(Error401Params{Status: problem.Status, Title: problem.Title,
				Detail: problem.Message, Code: problem.Code, Fields: fields})
		case 403:
			return Error403(Error403Params{Status: problem.Status, Title: problem.Title,
				Detail: problem.Message, Code: problem.Code, Fields: fields})
		case 404:
			return Error404(Error404Params{Status: problem.Status, Title: problem.Title,
				Detail: problem.Message, Code: problem.Code, Fields: fields})
		case 409:
			return Error409(Error409Params{Status: problem.Status, Title: problem.Title,
				Detail: problem.Message, Code: problem.Code, Fields: fields})
		case 413:
			return Error413(Error413Params{Status: problem.Status, Title: problem.Title,
				Detail: problem.Message, Code: problem.Code, Fields: fields})
		case 429:
			return Error429(Error429Params{Status: problem.Status, Title: problem.Title,
				Detail: problem.Message, Code: problem.Code, Fields: fields})
		default:
			return Error500(Error500Params{Status: problem.Status, Title: problem.Title,
				Detail: problem.Message, Code: problem.Code, Fields: fields})
		}
	})
}
`
}

func errorTemplate(pkg, component, title string) string {
	// The parameters are the api:error-renderer model. How much of it arrives
	// filled in is the framework's decision, not this template's: outside
	// development it hands over the status and the title and nothing else, so
	// the same file serves a developer and the public without a branch here.
	return "package " + pkg + `

export component ` + component + `(status: int, title: string, detail: string, code: string, requestId: string, fields: string[]): html {
  <main class="error">
    <p class="error-status">{status}</p>
    <h1 class="error-title">` + title + `</h1>
    {if detail != ''}<p class="error-detail">{detail}</p>{/if}
    <ul class="error-fields">
    {for field in fields}
      <li>{field}</li>
    {/for}
    </ul>
    {if code != ''}<p class="error-code">{code}</p>{/if}
    {if requestId != ''}<p class="error-request">Request {requestId}</p>{/if}
  </main>
}
`
}

func frameworkModuleDirective() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == "github.com/shibukawa/popcornweb" &&
		info.Main.Version != "" && info.Main.Version != "(devel)" && !strings.Contains(info.Main.Version, "+dirty") {
		return "require github.com/shibukawa/popcornweb " + info.Main.Version + "\n"
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "require github.com/shibukawa/popcornweb latest\n"
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return "require github.com/shibukawa/popcornweb v0.0.0\n\nreplace github.com/shibukawa/popcornweb => " + filepath.ToSlash(root) + "\n"
}

// firstBuild excludes an authored file from the second build.
//
// It is written only for a project that declared one, so a project that did not
// gets the file it always got. What it marks is the file granularity
// decision:transport-source-transform requires: a build tag excludes a whole
// file rather than a declaration, so a file holding a transport handler must
// hold nothing the other build needs. That is why the request and response
// types below sit in files of their own rather than beside the handler that
// reads them — the derived handler reads them too.
func firstBuild(options initOptions) string {
	if !options.FastHTTP {
		return ""
	}
	return "//go:build !fasthttp\n\n"
}

// secondBuildFiles are what a project declaring the fasthttp backend gets
// beyond the first build's: its entry point.
//
// Nothing else is needed. The handlers are derived, their binders are
// generated, the routes an application registered itself are emitted onto
// pwfast.RouteInstaller, and a page tree emits its own registry — so what is
// left is the file that mounts them and starts the runtime, which is the one
// file an application owns outright.
func secondBuildFiles(options initOptions) map[string]string {
	if !options.FastHTTP {
		return nil
	}
	return map[string]string{"cmd/" + options.Name + "/main_fasthttp.go": fastMainScaffold(options)}
}

// fastMainScaffold is main.go for the second build.
//
// It reads as the near-twin of the first, and the differences are the lesson:
// the runtime is pwfast, the mux is this transport's, and the routes an
// application registered itself arrive through the generated RegisterRoutes
// rather than through the init that registered them — because that init names
// a mux this build does not have and a handler it gets from elsewhere.
func fastMainScaffold(options initOptions) string {
	name := options.Name
	registered, discovered := defaultRegisteredDir, defaultDiscoveredDir
	registeredPkg, discoveredPkg := goPackageIdentifier(registered), goPackageIdentifier(discovered)
	imports := "\t\"context\"\n\t\"log\"\n\n"
	body := "\tmux := pwfast.NewServeMux()\n"
	switch router := effectiveRouter(options.Router); {
	case router == routerRegistered:
		imports += "\t\"" + name + "/" + registered + "\"\n"
		body += "\t" + registeredPkg + ".RegisterRoutes(pwfast.Routes(mux))\n"
	case router == routerDiscovered:
		imports += "\t\"" + name + "/" + discovered + "\"\n"
		if servesBrowserLogin(options) {
			imports += "\t\"" + name + "/" + registered + "\"\n"
		}
		body += "\t" + discoveredPkg + ".Register(mux)\n"
	default:
		imports += "\t\"" + name + "/" + registered + "\"\n\t\"" + name + "/" + discovered + "\"\n"
		body += "\t" + registeredPkg + ".RegisterRoutes(pwfast.Routes(mux))\n\t" +
			discoveredPkg + ".Register(mux)\n"
	}
	run := "pwfast.Run(context.Background(), mux.Handler)"
	if options.Auth != authNone {
		// The other build gains these frames from an import; this one cannot,
		// because a chain assembled from arguments does not silently gain a
		// frame. So it names them.
		imports += "\t\"github.com/shibukawa/popcornweb/plugin/auth/authfast\"\n"
		run = "pwfast.Run(context.Background(), mux.Handler, pwfast.WithSetup(authfast.Contribute))"
	}
	imports += "\t\"github.com/shibukawa/popcornweb/pwfast\""
	return `//go:build fasthttp

package main

import (
` + imports + databaseDriverImport(options) + storeMiddlewareImport(options) + sessionBackendImport(options) + `
)

func main() {
	// The same generated schemas the other build serves, so both transports
	// publish one API description.
	if err := pwfast.SetOpenAPIInfo(pwfast.OpenAPIInfo{Title: "` + name + `", Version: "0.1.0"}); err != nil {
		log.Fatal(err)
	}
` + authBootstrap(options) + `
` + body + `	if err := ` + run + `; err != nil {
		log.Fatal(err)
	}
}
`
}

// homeInputScaffold is the request type the starter route reads.
//
// It is a file of its own rather than a declaration beside the handler,
// because the derived handler reads it too and a build tag excludes a whole
// file. A project with no second build gets it here anyway: one layout is
// easier to teach than two, and this is the one the framework's own rule asks
// for.
func homeInputScaffold() string {
	return `package handlers

// homeInput is what this route reads from the request.
type homeInput struct {
	// Name is who the page greets. Anything the request does not carry falls
	// back to the declared default.
	Name string ` + "`query:\"name\" default:\"World\"`" + `
}
`
}

// identityScaffold is the response type the bearer route answers with, in a
// file of its own for the same reason homeInput is.
func identityScaffold() string {
	return `package handlers

// identity is what this route answers. It is an ordinary struct: pw generate
// emits the encoder and the OpenAPI schema from it, so nothing here writes
// JSON by hand.
type identity struct {
	Subject string   ` + "`json:\"subject\"`" + `
	Method  string   ` + "`json:\"method\"`" + `
	Scope   []string ` + "`json:\"scope,omitempty\"`" + `
}
`
}
