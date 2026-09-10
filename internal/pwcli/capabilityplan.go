package pwcli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/sessionstore"
)

// capabilityPlan is everything installing a capability would do. It is computed
// in full before anything is written, so a step that cannot succeed stops the
// command instead of leaving a half-installed capability in the project.
type capabilityPlan struct {
	// creates maps a project-relative path to the file to write. A destination
	// that already exists is a conflict, never an overwrite.
	creates map[string]string
	// appends maps a project-relative path to text added at its end. Appending
	// rather than rewriting keeps operator comments and tuned values.
	appends map[string]string
	// edits maps a project-relative path to its full replacement, for the few
	// changes that have to land inside an existing structure.
	edits map[string]string
	// directories are created empty, for a capability whose files are the
	// project's to write.
	directories []string
	// manual lists what the framework will not do to an application-owned file.
	manual []string
	// next lists the commands that finish the installation.
	next []string
	// generate reports whether the capability added generated sources.
	generate bool
}

func newCapabilityPlan() *capabilityPlan {
	return &capabilityPlan{
		creates: map[string]string{},
		appends: map[string]string{},
		edits:   map[string]string{},
	}
}

// summary lists every change, which is what the review screen shows: the
// operator approves the effect on the project, not just the answers.
func (p *capabilityPlan) summary() []string {
	var lines []string
	for _, path := range sortedKeys(p.creates) {
		lines = append(lines, "create  "+path)
	}
	for _, path := range p.directories {
		lines = append(lines, "create  "+path+"/")
	}
	for _, path := range sortedKeys(p.appends) {
		lines = append(lines, "append  "+path)
	}
	for _, path := range sortedKeys(p.edits) {
		lines = append(lines, "edit    "+path)
	}
	for _, line := range p.manual {
		lines = append(lines, "by hand "+line)
	}
	for _, command := range p.next {
		lines = append(lines, "then    "+command)
	}
	return lines
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// changes turns the plan into the atomic write applyFileChanges performs. A
// destination that already exists stops the command here, before a single
// file has been touched.
func (p *capabilityPlan) changes(root string) ([]fileChange, error) {
	var changes []fileChange
	for _, path := range sortedKeys(p.creates) {
		target := filepath.Join(root, filepath.FromSlash(path))
		if _, err := os.Stat(target); err == nil {
			return nil, fmt.Errorf("%s already exists; the framework never overwrites a file the application owns", path)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		// Created sources are canonical from the start, on the same terms as
		// the pw init scaffold. Appends and edits stay untouched: they land in
		// files the application owns, and reformatting one is more change than
		// this command was asked for.
		changes = append(changes, fileChange{path: target, source: []byte(canonicalScaffoldSource(path, p.creates[path]))})
	}
	for _, path := range sortedKeys(p.appends) {
		target := filepath.Join(root, filepath.FromSlash(path))
		current, err := os.ReadFile(target)
		if err != nil {
			return nil, err
		}
		changes = append(changes, fileChange{path: target, source: append(current, p.appends[path]...)})
	}
	for _, path := range sortedKeys(p.edits) {
		changes = append(changes, fileChange{
			path:   filepath.Join(root, filepath.FromSlash(path)),
			source: []byte(p.edits[path]),
		})
	}
	return changes, nil
}

// apply creates the directories and then writes every change at once.
func (p *capabilityPlan) apply(root string) error {
	changes, err := p.changes(root)
	if err != nil {
		return err
	}
	for _, directory := range p.directories {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			return err
		}
	}
	for _, change := range changes {
		if err := os.MkdirAll(filepath.Dir(change.path), 0o755); err != nil {
			return err
		}
	}
	return applyFileChanges(changes)
}

// planCapability computes what installing one capability would do.
func planCapability(state projectState, options addOptions) (*capabilityPlan, error) {
	plan := newCapabilityPlan()
	switch options.Capability {
	case capabilityDevbox:
		return plan, planDevbox(state, plan)
	case capabilityDatabase:
		return plan, planDatabase(state, options, plan)
	case capabilityDynamo:
		return plan, planDynamo(state, plan)
	case capabilityFirestore:
		return plan, planFirestore(state, plan)
	case capabilityRedis:
		return plan, planRedisValkey(state, plan)
	case capabilityAuth:
		return plan, planAuth(state, options, plan)
	case capabilityTailwind:
		return plan, planTailwind(state, plan)
	case capabilityImages:
		return plan, planImages(state, plan)
	case capabilityDiscovered:
		return plan, planPages(state, plan)
	case capabilityRegistered:
		return plan, planHandlers(state, plan)
	}
	return nil, fmt.Errorf("unknown capability %q", options.Capability)
}

// planDatabase configures the pool and, for a project that has neither yet,
// scaffolds the same starter schema and query api:cli-init writes.
func planDatabase(state projectState, options addOptions, plan *capabilityPlan) error {
	engine := engineFor(options.Engine)
	for _, name := range state.configFiles {
		// Every environment gets the pool, and only dev gets the local DSN:
		// the file for a deployment names the environment variable the
		// deployment sets.
		plan.appends[name] = databaseRuntimeSection(environmentToken(name), options.databaseDSN(state.config.Name), engine)
	}
	migrations := state.config.Migration.Dir
	if len(state.migrations) == 0 {
		plan.creates[migrations+"/00001_init.sql"] = engine.Schema
	} else {
		plan.directories = append(plan.directories, migrations)
	}
	// The SQL example needs a purpose to read it, and generate.queries has no
	// default, so the directory and its entry are written together or not at
	// all.
	document, err := os.ReadFile(filepath.Join(state.root, "popcornweb.toml"))
	if err != nil {
		return err
	}
	edited := string(document)
	if len(state.config.Generate.Queries) == 0 {
		plan.creates["queries/users.pw.sql"] = starterQuery()
		if edited, err = setGeneratePurpose(state, capabilityQueriesPurpose, []string{"queries"}); err != nil {
			return err
		}
		plan.generate = true
	}
	// Generation needs the dialect its .pw.sql sources are written in, and the
	// engine question is the only place a project states it.
	if edited, err = setProjectDatabase(edited, options.Engine); err != nil {
		return err
	}
	plan.edits["popcornweb.toml"] = edited
	// The entry point is application-owned, so the blank import that links the
	// selected engine is planned as an explicit edit.
	if engine.DriverImport != "" {
		if err := planEntryPointEdit(state, plan,
			[]blankImport{{engine.DriverImport, "the engine the configured DSN names"}}, "", ""); err != nil {
			return err
		}
	}
	if engine.DevboxPackage != "" && state.devbox != "" {
		edited, err := addDevboxPackage(state.devbox, engine.DevboxPackage)
		if err != nil {
			return err
		}
		plan.edits["devbox.json"] = edited
	}
	plan.next = append(plan.next, "pw migrate up")
	return nil
}

// planDevbox writes the development environment, carrying the packages this
// project already needs: the toolchain it was scaffolded for, and Tailwind when
// it is enabled. Valkey arrives with its own capability.
func planDevbox(state projectState, plan *capabilityPlan) error {
	packages := []string{"go@latest"}
	if state.config.Toolchain == toolchainTinyGo {
		packages = append(packages, "tinygo@latest")
	}
	if state.config.Tailwind.Enabled {
		packages = append(packages, tailwindDevboxPackage)
	}
	plan.creates["devbox.json"] = devboxScaffold(packages)
	plan.creates["devbox.lock"] = "{}\n"
	plan.next = append(plan.next, "devbox shell")
	return nil
}

// planRedisValkey adds the development server to the Devbox environment.
// planDynamo installs the DynamoDB store: its configuration, the starter record
// the generator reads, and the local server pw dev starts. It writes no
// migration, because the DynamoDB schema is the set of registered table
// definitions and has no version sequence to add a file to.
func planDynamo(state projectState, plan *capabilityPlan) error {
	for _, name := range state.configFiles {
		plan.appends[name] = dynamoRuntimeSection()
	}
	// The records directory and the purpose that reads it are written together:
	// generate.dynamo has no default, so a directory no purpose lists is a
	// directory nothing generates from.
	plan.creates[defaultDynamoDir+"/note.go"] = dynamoRecordScaffold()
	edited, err := setGeneratePurpose(state, capabilityDynamoPurpose, []string{defaultDynamoDir})
	if err != nil {
		return err
	}
	plan.edits["popcornweb.toml"] = edited
	plan.generate = true
	// The entry point is application-owned, so the import that installs the
	// client middleware is printed rather than injected.
	if err := planEntryPointEdit(state, plan,
		[]blankImport{{"github.com/shibukawa/popcornweb/database/dynamo", "installs the DynamoDB client middleware"}},
		"", ""); err != nil {
		return err
	}
	if state.devbox != "" {
		devbox, err := addDevboxPackage(state.devbox, dynamoDevboxPackage)
		if err != nil {
			return err
		}
		plan.edits["devbox.json"] = devbox
		plan.next = append(plan.next, "devbox shell")
	}
	return nil
}

// planFirestore installs the Firestore store: its configuration and the import
// that opens the client.
//
// It writes no migration. A Datastore kind is created by the first write, so
// there is no schema to add a file to.
//
// It adds no Devbox package either. The Datastore emulator is a Java process
// inside the Cloud SDK rather than a standalone server, so the configuration
// names the command to run instead of promising one pw dev starts.
func planFirestore(state projectState, plan *capabilityPlan) error {
	for _, name := range state.configFiles {
		plan.appends[name] = firestoreRuntimeSection()
	}
	// The entities directory and the purpose that reads it are written
	// together: generate.firestore has no default, so a directory no purpose
	// lists is a directory nothing generates from.
	plan.creates[defaultFirestoreDir+"/note.go"] = firestoreEntityScaffold()
	plan.creates[defaultFirestoreDir+"/notes.pw.firestore"] = firestoreQueryScaffold()
	edited, err := setGeneratePurpose(state, capabilityFirestorePurpose, []string{defaultFirestoreDir})
	if err != nil {
		return err
	}
	plan.edits["popcornweb.toml"] = edited
	plan.generate = true
	// The entry point is application-owned, so the import that installs the
	// client middleware is printed rather than injected.
	if err := planEntryPointEdit(state, plan,
		[]blankImport{{"github.com/shibukawa/popcornweb/database/firestore", "installs the Firestore client middleware"}},
		"", ""); err != nil {
		return err
	}
	plan.next = append(plan.next,
		"gcloud beta emulators datastore start --host-port=127.0.0.1:8081")
	return nil
}

// planEntryPointEdit adds imports and an optional call to the application entry
// point, so a capability installed later reaches the file state one installed at
// bootstrap already has.
//
// A file this cannot edit — an unparenthesized import block, no func main, a
// package that does not parse — falls back to printing the step. The operator
// still learns what is missing, which is what the command did for every
// capability before this.
func planEntryPointEdit(state projectState, plan *capabilityPlan, imports []blankImport, call, callComment string) error {
	path, source, err := entryPointSource(state.root, state.config.Main)
	if err != nil {
		printEntryPointSteps(plan, state.config.Main, imports, call)
		return nil
	}
	// An edit already planned for this file is the one to build on: two
	// capabilities in one run would otherwise each start from what is on disk
	// and the second would discard the first.
	if planned, ok := plan.edits[path]; ok {
		source = planned
	}
	edited, err := withBlankImports(source, imports...)
	if err != nil {
		printEntryPointSteps(plan, state.config.Main, imports, call)
		return nil
	}
	if call != "" {
		edited, err = withMainCall(edited, call, callComment)
		if err != nil {
			printEntryPointSteps(plan, state.config.Main, imports, call)
			return nil
		}
	}
	if edited == source {
		// Everything was already there, so there is nothing to show on the
		// review screen and nothing to write.
		return nil
	}
	plan.edits[path] = edited
	return nil
}

// printEntryPointSteps is the fallback for a file the command will not touch.
func printEntryPointSteps(plan *capabilityPlan, mainPackage string, imports []blankImport, call string) {
	for _, wanted := range imports {
		plan.manual = append(plan.manual, `add import _ "`+wanted.path+`" to `+mainPackage)
	}
	if call != "" {
		plan.manual = append(plan.manual, "call "+call+" in "+mainPackage+" before pw.Run")
	}
}

func planRedisValkey(state projectState, plan *capabilityPlan) error {
	edited, err := addDevboxPackage(state.devbox, "valkey@latest")
	if err != nil {
		return err
	}
	plan.edits["devbox.json"] = edited
	plan.next = append(plan.next, "devbox shell")
	return nil
}

// planAuth installs a login. The framework serves the endpoints itself, so the
// application-side wiring is the account resolver and one call in main.
func planAuth(state projectState, options addOptions, plan *capabilityPlan) error {
	version := state.nextMigrationVersion()
	migrations := state.config.Migration.Dir
	// The project already chose its engine, and no engine reads another's DDL.
	dialect := engineDialect(state.config.Database)
	sessionMigration, err := sessionstore.MigrationSQL(dialect, "popcornweb_session")
	if err != nil {
		return err
	}
	authMigration, err := auth.MigrationSQL(dialect)
	if err != nil {
		return err
	}
	plan.creates[migrations+"/"+migrationFileName(version, sessionstore.MigrationName)] = sessionMigration
	plan.creates[migrations+"/"+migrationFileName(version+1, auth.MigrationName)] = authMigration

	// pw add installs a provider login, OIDC or OAuth. A passkey mode
	// additionally needs a relying-party registration that depends on the
	// origin this deployment is reached on, which pw add cannot know, so it
	// stays a pw init answer.
	scaffold := initOptions{
		Name: state.config.Name, Auth: options.authMode(),
		// pw add installs the rdb backend, which is the one that fits a project
		// that already has a database. pw init offers the other two.
		Session: sessionRDB, AuthEmulator: options.AuthEmulator,
	}
	handlers := handlerPackageDirectory(state)
	resolver := handlers + "/accounts.go"
	plan.creates[resolver] = accountsScaffold(scaffold)

	section := authRuntimeConfig(scaffold)
	for _, name := range state.configFiles {
		plan.appends[name] = section
	}
	if options.AuthEmulator && usesOIDC(scaffold.Auth) {
		plan.creates[defaultIdPConfig] = devIdPRoster()
		// Built from scaffold rather than from a fresh value: this section is
		// written only for a mode that uses OIDC, and a hand-built options with
		// no mode in it reads as a project that has none, which silently
		// produced an empty append.
		emulator := scaffold
		emulator.AuthEmulator = true
		plan.appends["popcornweb.toml"] = devIdPProjectConfig(emulator)
	}
	// Storage is opt-in by blank import, so both stores this configuration
	// selects have to be linked by the application: the sessions and the
	// single-use login records. Naming a backend in configuration and leaving
	// the import out is a startup failure, which is why these are planned edits
	// rather than printed instructions.
	if err := planEntryPointEdit(state, plan,
		[]blankImport{
			{sessionBackendPlugin(sessionRDB, dialect), `serves session.backend = "rdb"`},
			{"github.com/shibukawa/popcornweb/authstate/" + dialect, "holds the single-use login records"},
		},
		goPackageIdentifier(handlers)+".RegisterAccounts()",
		"installed before Run: the framework calls these while it serves a login",
	); err != nil {
		return err
	}
	plan.next = append(plan.next, "pw migrate up")
	plan.generate = true
	return nil
}

// planTailwind wires the pinned toolchain in. The stylesheet link belongs in
// the application-owned document shell, so it is printed rather than injected.
func planTailwind(state projectState, plan *capabilityPlan) error {
	plan.creates[defaultTailwindInput] = tailwindEntryScaffold(state.config.Generate)
	plan.creates[defaultTailwindOutput] = "/* Generated by Tailwind CSS. */\n"
	plan.appends["popcornweb.toml"] = tailwindProjectConfig()
	if state.devbox == "" {
		// A project without the Devbox environment installs the toolchain its
		// own way, so naming the version is all the framework can do.
		plan.manual = append(plan.manual, "install "+tailwindToolchainRequirement)
	} else {
		edited, err := addDevboxPackage(state.devbox, tailwindDevboxPackage)
		if err != nil {
			return err
		}
		plan.edits["devbox.json"] = edited
	}
	plan.manual = append(plan.manual,
		`add <link rel="stylesheet" href="/`+defaultTailwindOutput+`"> to the document shell`)
	plan.next = append(plan.next, "devbox shell")
	return nil
}

// planImages turns on the conversion and installs the encoders it runs.
//
// The encoders are declared rather than downloaded: a build never installs a
// tool, so a project that took this capability and then removed the packages
// converts nothing and says so, instead of quietly fetching them.
func planImages(state projectState, plan *capabilityPlan) error {
	plan.appends["popcornweb.toml"] = imagesProjectConfig()
	if state.devbox == "" {
		plan.manual = append(plan.manual, "install "+imageToolchainRequirement)
		return nil
	}
	devbox := state.devbox
	for _, pkg := range imageDevboxPackages {
		edited, err := addDevboxPackage(devbox, pkg)
		if err != nil {
			return err
		}
		devbox = edited
	}
	plan.edits["devbox.json"] = devbox
	plan.next = append(plan.next, "devbox shell")
	return nil
}

// planPages installs a page tree: the same starter tree api:cli-init writes,
// and the purpose that makes it generate. The two go together, because a tree
// no purpose lists is a directory nothing reads.
//
// The Register call is printed rather than injected, since the entry point is
// application-owned like every other main.go edit this command would rather not
// make.
func planPages(state projectState, plan *capabilityPlan) error {
	root := defaultDiscoveredDir
	// The starter page describes the project it was written into, so the tree
	// this command adds has to be built from the same answers pw init would
	// have had. Reading them back from the project is what keeps the two paths
	// on one file state rather than on two pages that only look alike.
	options, err := scaffoldOptionsOf(state)
	if err != nil {
		return err
	}
	// The tree is not in the project yet, so the probes cannot see it. This
	// command is what puts it there.
	options.Router = withRouter(options.Router, routerDiscovered)
	for path, source := range pageTreeScaffold(options, root) {
		plan.creates[path] = source
	}
	edited, err := setPagesPurpose(state, []string{root})
	if err != nil {
		return err
	}
	plan.edits["popcornweb.toml"] = edited
	plan.manual = append(plan.manual,
		"import "+state.config.Name+"/"+root+" from "+state.config.Main+
			" and call "+goPackageIdentifier(root)+".Register on its mux")
	plan.generate = true
	return nil
}

// planHandlers installs the registered router into a project that started with
// the page tree alone: the package, its mux, and one route example.
//
// The template purpose gains the same directory, because a page template sits
// beside the handler that renders it, and the mounting is printed for the same
// reason a page tree's Register call is.
func planHandlers(state projectState, plan *capabilityPlan) error {
	directory := defaultRegisteredDir
	options := initOptions{
		Name:   state.config.Name,
		TinyGo: state.config.Toolchain == toolchainTinyGo,
		Auth:   authNone,
	}
	for path, source := range registeredRouterScaffold(options, directory) {
		plan.creates[path] = source
	}

	edited, err := setGeneratePurpose(state, "handlers", []string{directory})
	if err != nil {
		return err
	}
	templates := append([]string{directory}, state.config.Generate.Templates...)
	sort.Strings(templates)
	templates = slices.Compact(templates)
	if edited, err = setGeneratePurposeIn(edited, "templates", templates); err != nil {
		return err
	}
	plan.edits["popcornweb.toml"] = edited
	plan.manual = append(plan.manual,
		"import "+state.config.Name+"/"+directory+" from "+state.config.Main+" and serve its Handlers()")
	plan.generate = true
	return nil
}

// handlerPackageDirectory picks where an application-side source belongs. The
// handler purpose is the answer by construction: it is the list of directories
// api:cli-generate reads for routes.
func handlerPackageDirectory(state projectState) string {
	if len(state.config.Generate.Handlers) > 0 {
		return state.config.Generate.Handlers[0]
	}
	return "handlers"
}

// goPackageIdentifier names the package a directory holds, for a printed
// instruction that has to compile once the operator follows it.
func goPackageIdentifier(directory string) string {
	base := filepath.Base(filepath.FromSlash(directory))
	return strings.NewReplacer("-", "", ".", "").Replace(base)
}

func starterQuery() string {
	// Commented out with the migration it reads. A live statement against a
	// table the starter migration no longer creates would generate a function
	// that fails on its first call, which is worse than an example the reader
	// has to uncomment.
	return `package queries

// A typed query. Uncomment it together with the example table in
// migrations/00001_init.sql, and pw generate emits a Go function whose
// arguments and result come from the statement below.
//
// type Example {
//   id: int
//   name: string
// }
//
// export statement FindExample(id: int): sql.one<Example> {
// SELECT id, name FROM example WHERE id = {id}
// }
`
}
