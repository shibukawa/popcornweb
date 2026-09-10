package pwcli

import (
	tea "github.com/charmbracelet/bubbletea"
)

// initWizardSteps builds the question list, seeding every answer from the
// shortcut flags that were already supplied on the command line.
func initWizardSteps(defaults initOptions) []wizardStep[initOptions] {
	steps := []wizardStep[initOptions]{
		newChoiceStep(
			"Preset",
			"A named set of answers for a project shape. Every answer it gives is on the next "+
				"screen and every one of them is still editable, so this is a starting point rather than a mode.",
			presetCursor(defaults.Preset),
			presetChoices()...,
		),
		// A package is named by the module path a consumer imports, because a
		// project named myapp and published at github.com/someone/myapp would
		// have to be renamed before its first release.
		when(isApplication,
			newTextStep(
				"Project name",
				"Creates ./<name> holding a Go module of the same name.",
				defaults.Name,
				"myapp",
				validateProjectName,
				func(target *initOptions, value string) { target.Name = value },
			),
		),
		when(func(options initOptions) bool { return options.Kind == kindPackage },
			newTextStep(
				"Module path",
				"The path a consumer imports. The directory is its last element.",
				defaults.Name,
				"github.com/you/mycomponent",
				validateModulePath,
				func(target *initOptions, value string) { target.Name = value },
			),
		),
	}
	// Every capability question describes an application, so a package project
	// skips all of them rather than answering them. Wrapping them together
	// keeps that one rule in one place instead of on each question.
	for _, step := range applicationSteps(defaults) {
		steps = append(steps, when(isApplication, step))
	}
	// Asked for both kinds and after everything else, because it describes the
	// machines this project is edited on rather than the project itself — the
	// same territory as the editor files every kind already gets.
	steps = append(steps, newChoiceStep(
		"AI coding agent",
		"Places the bundled framework skill — template and query syntax, project anatomy, and the "+
			"pw commands that check an edit — where your coding agent discovers it.",
		skillsCursor(defaults.Skills),
		wizardChoice[initOptions]{
			name:        ".claude",
			description: ".claude/skills/" + agentSkillDir + "/ — the directory Claude Code reads",
			apply:       func(target *initOptions) { target.Skills = skillsClaude },
		},
		wizardChoice[initOptions]{
			name:        ".agents",
			description: ".agents/skills/" + agentSkillDir + "/ — the shared layout other coding agents read",
			apply:       func(target *initOptions) { target.Skills = skillsAgents },
		},
		wizardChoice[initOptions]{
			name:        "None",
			description: "no agent files; copy skills/ from the framework repository later if one arrives",
			apply:       func(target *initOptions) { target.Skills = skillsNone },
		},
	))
	return steps
}

// skillsCursor preselects the agent directory a seeded answer names.
func skillsCursor(value string) int {
	switch value {
	case skillsAgents:
		return 1
	case skillsNone:
		return 2
	}
	return 0
}

// isApplication reports whether the capability questions apply to this project
// kind. Everything they decide belongs to a binary, and a package produces none.
func isApplication(options initOptions) bool { return options.Kind != kindPackage }

// applicationSteps is the capability question list, in the order
// decision:interactive-project-bootstrap fixed: what shapes the project first,
// and how this machine gets its tools last.
func applicationSteps(defaults initOptions) []wizardStep[initOptions] {
	return []wizardStep[initOptions]{
		newChoiceStep(
			"TinyGo support",
			"TinyGo produces much smaller binaries and has the more complete wasm target. "+
				"pw add cannot change this later; switching is a manual edit, see the pw init docs.",
			yesNoCursor(defaults.TinyGo),
			wizardChoice[initOptions]{
				name:        "Yes",
				description: "pw.ServeMux routing plus the TinyGo toolchain in devbox.json",
				apply:       func(target *initOptions) { target.TinyGo = true },
			},
			wizardChoice[initOptions]{
				name:        "No",
				description: "net/http.ServeMux routing, host Go toolchain only",
				apply:       func(target *initOptions) { target.TinyGo = false },
			},
		),
		// Asked next to the toolchain because it is the same kind of answer: a
		// build-time property of the source tree that everything below inherits.
		// It is not a router question — both routers work either way — and it is
		// not exclusive with the toolchain, so it gets its own step rather than a
		// fourth option on one of theirs.
		newChoiceStep(
			"fasthttp backend",
			"Builds this project for fasthttp in addition to net/http. Handlers stay net/http and "+
				"generation derives the fasthttp copy from them, so a handler is written once. "+
				"A transport handler has to sit in a file of its own for that to work. "+
				"Take it only if that second build is planned.",
			yesNoCursor(defaults.FastHTTP),
			wizardChoice[initOptions]{
				name:        "Yes",
				description: "project.fasthttp = true, and generation emits both builds",
				apply:       func(target *initOptions) { target.FastHTTP = true },
			},
			wizardChoice[initOptions]{
				name:        "No",
				description: "net/http only, and generated files carry no build constraint",
				apply:       func(target *initOptions) { target.FastHTTP = false },
			},
		),
		newChoiceStep(
			"Router",
			"Which routers this project starts with. They coexist on one mux, pw add installs "+
				"the other one later, and the directory each reads is a popcornweb.toml value.",
			routerCursor(defaults.Router),
			wizardChoice[initOptions]{
				name:        "Registered",
				description: defaultRegisteredDir + "/: routes written in Go, any method, generated OpenAPI",
				apply:       func(target *initOptions) { target.Router = routerRegistered },
			},
			wizardChoice[initOptions]{
				name:        "Discovered",
				description: defaultDiscoveredDir + "/: a directory with a page template is a route; for an HTML website",
				apply:       func(target *initOptions) { target.Router = routerDiscovered },
			},
			wizardChoice[initOptions]{
				name:        "Both",
				description: "an API in " + defaultRegisteredDir + "/ and a website in " + defaultDiscoveredDir + "/, on one mux",
				apply:       func(target *initOptions) { target.Router = routerBoth },
			},
		),
		newChoiceStep(
			"Tailwind CSS",
			"Wires the pinned Tailwind toolchain into the project and generates public/generated/app.css.",
			yesNoCursor(defaults.Tailwind),
			wizardChoice[initOptions]{
				name:        "Yes",
				description: "assets/app.css entry point and the Tailwind build step",
				apply:       func(target *initOptions) { target.Tailwind = true },
			},
			wizardChoice[initOptions]{
				name:        "No",
				description: "plain CSS owned by the application; pw add tailwind enables it later",
				apply:       func(target *initOptions) { target.Tailwind = false },
			},
		),
		// Authentication is asked before the stores rather than after them,
		// because it is the answer that decides whether a store is optional at
		// all. Asked the other way round it was a question a project could skip
		// past without ever seeing.
		//
		// A bearer project skips it entirely. Its four answers cannot express
		// jwt_only — deliberately, since that mode is reached by naming the
		// project shape it belongs to rather than by answering the question
		// every browser application also answers — and a row that cannot show
		// this project's mode would quietly replace it with one it can.
		when(func(options initOptions) bool { return options.Auth != authJWTOnly }, newChoiceStep(
			"Authentication",
			"Selects the login model. The framework writes the [auth] configuration; handlers stay yours. "+
				"A login needs somewhere to keep its sessions, so the next question is about that.",
			authCursor(defaults.Auth),
			wizardChoice[initOptions]{
				name:        "None",
				description: "no authentication configuration; pw add auth enables it later",
				apply:       setAuth(authNone),
			},
			wizardChoice[initOptions]{
				name:        "OIDC",
				description: "log in against an OpenID Provider",
				apply:       setAuth(authOIDC),
			},
			wizardChoice[initOptions]{
				name:        "OIDC and passkey",
				description: "the provider bootstraps the account; a passkey is the everyday login",
				apply:       setAuth(authOIDCPasskey),
			},
			wizardChoice[initOptions]{
				name:        "Passkey only",
				description: "no provider; an administrator issues the first sign-in credential",
				apply:       setAuth(authPasskey),
			},
			wizardChoice[initOptions]{
				name:        "OAuth provider",
				description: "log in through a provider that issues no ID Token, such as X; no local emulator",
				apply:       setAuth(authOAuth),
			},
		)),
		// With a login there is no "no store" answer to give, so this asks
		// which one rather than whether.
		when(servesBrowserLogin,
			newChoiceStep(
				"Store",
				"A login has to keep its sessions somewhere, so this is which store rather than whether. "+
					"The next question covers the other kind.",
				authStoreCursor(defaults),
				authStoreChoices()...,
			),
		),
		// Without a login every store is optional, so the questions go back to
		// asking whether.
		when(func(options initOptions) bool { return !servesBrowserLogin(options) },
			newChoiceStep(
				"Database",
				"Adds the rdb configuration, the migration directory, and a typed SQL example.",
				yesNoCursor(defaults.Database),
				wizardChoice[initOptions]{
					name:        "Yes",
					description: "[middleware.rdb], migrations/00001_init.sql, and a typed SQL example",
					apply:       setDatabase(true),
				},
				wizardChoice[initOptions]{
					name:        "No",
					description: "no database, no SQL example, and no migrations; pw add database enables it later",
					apply:       setDatabase(false),
				},
			),
		),
		when(func(options initOptions) bool { return !servesBrowserLogin(options) && options.Database },
			newChoiceStep(
				"Database engine",
				"Decides the DSN, the dialect of the starter schema and migrations, and the development server. "+
					"Changing it later means rewriting both, so pw add cannot do it for you.",
				engineCursor(defaults.Engine),
				engineChoices()...,
			),
		),
		// DynamoDB holds the login on its own, through auth.backend = "dynamo",
		// so choosing it asks for no SQL engine to carry what it cannot.
		// The other half of the store pair, asked wherever DynamoDB was not
		// already the login answer.
		when(func(options initOptions) bool {
			return !servesBrowserLogin(options) || options.AuthStore != dynamoStore
		},
			newChoiceStep(
				"DynamoDB",
				"A second kind of store, not a fourth SQL engine. It combines with any database answer, "+
					"including none, and brings its own typed records and local development server.",
				yesNoCursor(defaults.Dynamo),
				wizardChoice[initOptions]{
					name:        "Yes",
					description: "[middleware.dynamo], a records/ starter type, and dynamodb-local in devbox.json",
					apply:       func(target *initOptions) { target.Dynamo = true },
				},
				wizardChoice[initOptions]{
					name:        "No",
					description: "no [middleware.dynamo] section; pw add dynamo enables it later",
					apply:       func(target *initOptions) { target.Dynamo = false },
				},
			),
		),
		// The Google Cloud half of the same answer, on the same terms.
		when(func(options initOptions) bool {
			return !servesBrowserLogin(options) || options.AuthStore != firestoreStore
		},
			newChoiceStep(
				"Firestore",
				"The same kind of answer as DynamoDB, in Datastore mode on Google Cloud. The database it names "+
					"must have been created in Datastore mode, which is chosen at creation and cannot be changed.",
				yesNoCursor(defaults.Firestore),
				wizardChoice[initOptions]{
					name:        "Yes",
					description: "[middleware.firestore], pointed at the local Datastore emulator",
					apply:       func(target *initOptions) { target.Firestore = true },
				},
				wizardChoice[initOptions]{
					name:        "No",
					description: "no [middleware.firestore] section; pw add firestore enables it later",
					apply:       func(target *initOptions) { target.Firestore = false },
				},
			),
		),
		when(servesBrowserLogin,
			newChoiceStep(
				"Session storage",
				"Where a login session lives. Every choice reads the same in handlers; "+
					"general server backends are added by blank imports, while development modes are built in.",
				sessionCursor(defaults),
				wizardChoice[initOptions]{
					name:        "Development, reset on restart",
					description: "process-local and revocable; no sealed record cookie; accepted only in dev",
					apply:       setSession(sessionDevVolatile),
				},
				wizardChoice[initOptions]{
					name:        "Development, keep on restart",
					description: "sealed browser record with a stable keyring; accepted only in dev",
					apply:       setSession(sessionDevPersist),
				},
				wizardChoice[initOptions]{
					name:        "Database",
					description: "one row per session through sessionstore/sqlite; revocable, swept, carries a migration",
					apply:       setSession(sessionRDB),
				},
				wizardChoice[initOptions]{
					name:        "Cookie",
					description: "sealed into a second cookie; no storage and no import, but no revoking either",
					apply:       setSession(sessionCookie),
				},
				wizardChoice[initOptions]{
					name:        "Redis or Valkey",
					description: "server-side TTL through sessionstore/redis; revocable, nothing to sweep",
					apply:       setSession(sessionRedis),
				},
				wizardChoice[initOptions]{
					name:        "DynamoDB",
					description: "one item per session through sessionstore/dynamo; revocable, expired by table TTL",
					apply:       setSession(sessionDynamo),
				},
				wizardChoice[initOptions]{
					name:        "Firestore",
					description: "one entity per session through sessionstore/firestore; revocable, expired by a TTL policy",
					apply:       setSession(sessionFirestore),
				},
			),
		),
		when(func(options initOptions) bool { return usesOIDC(options.Auth) },
			newChoiceStep(
				"OIDC provider",
				"The local emulator signs you in by picking a user from a list, so login works before a real IdP exists.",
				yesNoCursor(defaults.AuthEmulator),
				wizardChoice[initOptions]{
					name:        "Local emulator",
					description: "pw dev runs it and injects the issuer and client credentials",
					apply:       func(target *initOptions) { target.AuthEmulator = true },
				},
				wizardChoice[initOptions]{
					name:        "External provider",
					description: "fill in auth.oidc yourself, or supply it through the environment",
					apply:       func(target *initOptions) { target.AuthEmulator = false },
				},
			),
		),
		newChoiceStep(
			"Devbox environment",
			"How this machine gets the toolchain and the services. Only the answer changes; the project is the same either way.",
			yesNoCursor(defaults.Devbox),
			wizardChoice[initOptions]{
				name:        "Yes",
				description: "devbox.json pins the versions, and pw dev starts the services it declares",
				apply:       func(target *initOptions) { target.Devbox = true },
			},
			wizardChoice[initOptions]{
				name:        "No",
				description: "keep your own setup — mise, Docker Compose, Nix, Homebrew, Scoop; pw add devbox enables it later",
				apply:       setDevbox(false),
			},
		),
		when(func(options initOptions) bool { return options.Devbox },
			newChoiceStep(
				"Redis or Valkey",
				"Adds the Valkey development server to devbox.json for session, token, and counter state.",
				yesNoCursor(defaults.Redis),
				wizardChoice[initOptions]{
					name:        "Yes",
					description: "pw dev starts Valkey beside the application",
					apply:       func(target *initOptions) { target.Redis = true },
				},
				wizardChoice[initOptions]{
					name:        "No",
					description: "a smaller development environment; pw add redis-valkey enables it later",
					apply:       func(target *initOptions) { target.Redis = false },
				},
			),
		),
	}
}

// engineChoices renders the engine table as wizard choices, in catalog order,
// so adding an engine costs one table entry rather than a step edit as well.
func engineChoices() []wizardChoice[initOptions] {
	choices := make([]wizardChoice[initOptions], 0, len(engineOrder))
	for _, name := range engineOrder {
		engine := databaseEngines[name]
		choices = append(choices, wizardChoice[initOptions]{
			name:        engine.Label,
			description: engine.Summary,
			apply:       setEngine(name),
		})
	}
	return choices
}

// authStoreChoices lists the stores a login can be built on. There is no "none"
// among them: a session has to live somewhere, and the question exists because
// that is already decided rather than to ask again whether it was.
func authStoreChoices() []wizardChoice[initOptions] {
	choices := make([]wizardChoice[initOptions], 0, len(engineOrder)+1)
	for _, name := range engineOrder {
		engine := databaseEngines[name]
		choices = append(choices, wizardChoice[initOptions]{
			name:        engine.Label,
			description: engine.Summary,
			apply:       setAuthStore(name),
		})
	}
	choices = append(choices, wizardChoice[initOptions]{
		name:        "DynamoDB",
		description: "the whole login in DynamoDB; no SQL database is scaffolded behind it",
		apply:       setAuthStore(dynamoStore),
	}, wizardChoice[initOptions]{
		name:        "Firestore",
		description: "the whole login in Firestore, in Datastore mode; no SQL database is scaffolded behind it",
		apply:       setAuthStore(firestoreStore),
	})
	return choices
}

// setAuthStore records which store the login was chosen through and applies
// what that store means.
func setAuthStore(store string) func(*initOptions) {
	return func(target *initOptions) {
		target.AuthStore = store
		switch store {
		case dynamoStore:
			// DynamoDB carries the whole login, so no relational database is
			// added behind the answer.
			target.Dynamo = true
			target.Firestore = false
			target.Database = false
		case firestoreStore:
			// Firestore carries it on the same terms. Only one of the two can,
			// because auth.backend names one store for all four of its kinds.
			target.Firestore = true
			target.Dynamo = false
			target.Database = false
		default:
			target.Database = true
			target.Engine = store
			target.Dynamo = false
			target.Firestore = false
		}
	}
}

// authStoreCursor preselects the store a seeded answer already describes.
func authStoreCursor(defaults initOptions) int {
	if defaults.AuthStore == firestoreStore || (defaults.Firestore && defaults.AuthStore == "") {
		return len(engineOrder) + 1
	}
	if defaults.AuthStore == dynamoStore || (defaults.Dynamo && defaults.AuthStore == "") {
		return len(engineOrder)
	}
	return engineCursor(defaults.Engine)
}

// setEngine records the engine answer.
func setEngine(name string) func(*initOptions) {
	return func(target *initOptions) { target.Engine = name }
}

// setDevbox records the answer and clears what depends on it. The Valkey
// question only ever writes a Devbox package, so a declined environment takes
// that step out of the wizard rather than leaving an answer nothing applies.
func setDevbox(enabled bool) func(*initOptions) {
	return func(target *initOptions) {
		target.Devbox = enabled
		if !enabled {
			target.Redis = false
		}
	}
}

// setDatabase records the answer and clears what depends on it. A declined
// database takes the authentication step out of the wizard, so an answer
// seeded by --auth must not survive as an unreachable one.
func setDatabase(enabled bool) func(*initOptions) {
	return func(target *initOptions) {
		target.Database = enabled
		if !enabled {
			// The engine step goes with it. Its answer applies only inside a
			// project that has a database, so leaving one behind would be an
			// answer to a question this project never reached.
			target.Engine = engineSQLite
			if target.Auth != authJWTOnly {
				// A browser login keeps its ceremony records and its allowlist
				// in SQL, so declining the database declines it too. The
				// bearer mode stores nothing and survives: it verifies a token
				// somebody else issued and creates no account.
				target.Auth = authNone
				target.AuthEmulator = false
			}
		}
	}
}

// setAuth records the mode and clears the emulator answer for a mode that has
// no provider, so a stray --devidp flag cannot survive the choice.
func setAuth(mode string) func(*initOptions) {
	return func(target *initOptions) {
		target.Auth = mode
		if !usesOIDC(mode) {
			target.AuthEmulator = false
		}
	}
}

// setSession records the backend and takes what it implies with it. A
// Redis-backed session needs the server that serves it, so the Devbox answer
// follows the storage answer rather than contradicting it.
func setSession(backend string) func(*initOptions) {
	return func(target *initOptions) {
		target.Session = backend
		target.SessionExplicit = true
		if backend == sessionRedis && target.Devbox {
			target.Redis = true
		}
	}
}

// sessionCursor maps a backend onto its position in the choice list.
func sessionCursor(options initOptions) int {
	if !options.SessionExplicit {
		return 0
	}
	switch options.Session {
	case sessionDevPersist:
		return 1
	case sessionRDB:
		return 2
	case sessionCookie:
		return 3
	case sessionRedis:
		return 4
	case sessionDynamo:
		return 5
	case sessionFirestore:
		return 6
	default:
		return 0
	}
}

// authCursor maps an authentication mode onto its position in the choice list.
func authCursor(mode string) int {
	switch mode {
	case authOIDC:
		return 1
	case authOIDCPasskey:
		return 2
	case authPasskey:
		return 3
	case authOAuth:
		return 4
	default:
		return 0
	}
}

// yesNoCursor maps a boolean default onto a leading yes, trailing no choice list.
// routerCursor preselects the router answer a shortcut flag already supplied.
func routerCursor(router string) int {
	switch effectiveRouter(router) {
	case routerDiscovered:
		return 1
	case routerBoth:
		return 2
	default:
		return 0
	}
}

func yesNoCursor(enabled bool) int {
	if enabled {
		return 0
	}
	return 1
}

// runInitWizard asks every question and returns the confirmed options.
func runInitWizard(defaults initOptions, programOptions ...tea.ProgramOption) (initOptions, error) {
	return runWizard(newInitWizard(defaults), programOptions...)
}

// newInitWizard builds the model. Unlike api:cli-add and api:cli-new, init
// reviews answers rather than files: nothing it writes can collide with work
// that already exists, so its review is a list that can be edited rather than
// one that can only be read.
//
// The preset and the name are walked, and everything after them is a row on
// that list. The answers are dependent — a login decides whether a store is
// optional, a store decides whether an engine is asked — so the run that most
// needs to go back is the run that understood a question only after seeing a
// later one.
func newInitWizard(defaults initOptions) wizardModel[initOptions] {
	return wizardModel[initOptions]{
		steps:    initWizardSteps(defaults),
		defaults: defaults,
		title:    "Popcorn Web  new project",
		confirm:  "create",
		theme:    newWizardTheme(),
		// The preset step and the name step. A named preset answers everything
		// after them, so those two are the whole of what it asks.
		linear:   2,
		rebuild:  initWizardSteps,
		editing:  hubNoStep,
		answered: map[int]bool{},
	}
}
