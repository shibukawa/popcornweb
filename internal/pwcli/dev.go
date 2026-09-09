package pwcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/internal/dbseed"
	"github.com/shibukawa/popcornweb/internal/devconsole"
	"github.com/shibukawa/popcornweb/internal/pwenv"
)

func runDev(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("dev: unexpected arguments")
	}
	root, err := projectRoot(".")
	if err != nil {
		return err
	}
	config, err := loadProjectConfig(root)
	if err != nil {
		return err
	}
	if err := refuseInPackage(config, "dev"); err != nil {
		return err
	}
	// The development loop always keeps the source maps. Nothing here is
	// deployed, and a browser stack trace that names the authored TypeScript is
	// the reason the script build emits a map at all.
	config.Assets.SourceMaps = true
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The viewer and the console start before anything the loop reports on, so
	// that the first phase already has somewhere to be published and the
	// developer can open the console while the first build is still running.
	// Neither failing is worth ending the loop over: the application still
	// runs, it is only unobserved.
	var telemetry *devTelemetryViewer
	if config.Otel.Enabled {
		telemetry, err = startDevTelemetryViewer(config, stdout)
		if err != nil {
			fmt.Fprintln(stderr, "pw dev: telemetry viewer:", err)
		}
		defer telemetry.close()
	}
	// The pane is registered before the harness exists, because the harness is
	// one of the things the first generation run produces.
	var storybook *devStorybook
	if config.Console.Storybook {
		storybook = &devStorybook{}
		defer storybook.stop()
	}
	// One token per run, generated rather than configured, so an announcement
	// is not accepted from anything that merely reached the port.
	attachToken, err := randomToken()
	if err != nil {
		return err
	}
	logs := newDevLogCapture(root, config, attachToken, stdout)
	attach := devconsole.NewAttachment(attachToken)
	console := startDevConsole(root, config, telemetry, storybook, attach, stdout, stderr)
	defer console.Close()
	// The console offers the seed datasets as an action rather than
	// implementing one: pw seed already exists, and this is the same call the
	// loop makes after a migration cycle empties the database.
	if config.Seed.Auto && hasSeedDatasets(root) {
		console.SetReseed(func(ctx context.Context) error { return runSeed(ctx, nil, stdout, stderr) })
	}

	// Startup spends its time on services, generation, migration, and a build,
	// and said nothing while it did. The region names the phase in progress and
	// gives way once the loop reaches its steady state.
	report := &devReporter{progress: newProgressRegion(stdout), console: console}
	report.Phase("starting services")
	stopServices := startDevboxServices(ctx, root, stdout, stderr)
	defer stopServices()

	report.Phase("generating")
	if _, err := generateProject(ctx, false, stdout, false); err != nil {
		report.Failed(err)
		return err
	}
	// The stylesheet is an input to the served tree rather than something served
	// beside it: the asset build clears dist/public and fills it from public/,
	// so a stylesheet written after that build is a file the run never serves.
	// It is the scaffolded placeholder that gets copied instead, and the first
	// page of a new project arrives with a stylesheet defining nothing it uses.
	//
	// pw build has always had this order. Only the loop had it the other way
	// round, which is why the project it happened to is a new one.
	var tailwind *exec.Cmd
	var tailwindExited <-chan error
	if config.Tailwind.Enabled {
		report.Phase("building CSS")
		development := config.Tailwind
		development.Minify = false
		if err := buildTailwind(ctx, root, development, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "pw dev:", err)
			report.Failed(err)
		}
		tailwind, tailwindExited, err = startTailwindWatch(ctx, root, development, stdout, stderr)
		if err != nil {
			report.Failed(err)
			return err
		}
		defer func() { stopCommand(tailwind, tailwindExited) }()
	}
	report.Phase("building assets")
	if assets, err := buildDerivedAssets(root, config.Assets); err != nil {
		// The loop survives an unbuildable state, here as everywhere else: the
		// next change is what fixes it.
		fmt.Fprintln(stderr, "pw dev:", err)
		report.Failed(err)
	} else {
		reportDerivedAssets(stdout, assets)
	}
	storybook.start(root, storybookStyles(config, readDevelopmentServer(root)), stdout, stderr)
	report.Phase("applying migrations")
	if err := runDevMigrations(ctx, root, config, stdout, stderr); err != nil {
		report.Failed(err)
		return err
	}
	var idp *devIdentityProvider
	if config.IdP.Enabled {
		report.Phase("starting the identity provider")
		idp, err = startDevIdentityProvider(ctx, root, config, stdout)
		if err != nil {
			report.Failed(err)
			return fmt.Errorf("pw dev: development identity provider: %w", err)
		}
		defer idp.close()
	}
	rosterState := idp.watchState()
	state, err := watchSnapshot(root, config, tailwind == nil)
	if err != nil {
		return err
	}
	report.Phase("building and starting the application")
	// One reader for the whole run: what it remembers between two application
	// processes is what makes the second one a reload rather than a startup.
	bootLog := newDevBootLog(stderr)
	app, exited, err := startApplication(ctx, root, config.Main, idp, telemetry, logs, console, config.Console, attachToken, bootLog, stdout, stderr)
	// The region gives way here: everything after this point is the application
	// and its services talking, which is the scrollback the loop exists to show.
	report.Done()
	if err != nil {
		report.Failed(err)
		return err
	}
	report.Healthy()
	defer func() { stopCommand(app, exited); bootLog.Flush() }()
	telemetry.monitor(app)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-exited:
			if ctx.Err() != nil {
				return nil
			}
			// Whatever the process was in the middle of saying reaches the
			// terminal before the loop says anything about it.
			bootLog.Flush()
			// An application that stopped is not a loop that should stop. The
			// developer loop exists to survive a half-finished edit, and a
			// project spends most of the time between two working states in a
			// state that does not build, start, or stay up. The next watched
			// change rebuilds and restarts it.
			app, exited = nil, nil
			if err == nil {
				fmt.Fprintln(stdout, "pw dev: the application exited; waiting for the next change")
				console.Publish("the application exited", devconsole.StatusStarting, nil)
				continue
			}
			fmt.Fprintln(stderr, "pw dev: application exited:", err)
			fmt.Fprintln(stderr, "pw dev: waiting for the next change")
			console.Failed("running the application", "the application exited: "+err.Error())
		case err := <-tailwindExited:
			if err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintln(stderr, "pw dev: tailwindcss exited:", err)
			}
			tailwind = nil
			tailwindExited = nil
			addWatchFile(state, filepath.Join(root, filepath.FromSlash(config.Tailwind.Input)))
		case <-ticker.C:
			// A roster edit reloads in place: restarting the provider would
			// invalidate the issuer and credentials the application holds.
			if current := idp.watchState(); current != rosterState {
				rosterState = current
				idp.reload(stdout, stderr)
				continue
			}
			next, err := watchSnapshot(root, config, tailwind == nil)
			if err != nil {
				fmt.Fprintln(stderr, "pw dev:", err)
				continue
			}
			if equalWatchState(state, next) {
				continue
			}
			stopCommand(app, exited)
			bootLog.Flush()
			app, exited = nil, nil
			if config.Tailwind.Enabled && tailwind == nil {
				development := config.Tailwind
				development.Minify = false
				if err := buildTailwind(ctx, root, development, stdout, stderr); err != nil {
					fmt.Fprintln(stderr, "pw dev:", err)
				}
				tailwind, tailwindExited, err = startTailwindWatch(ctx, root, development, stdout, stderr)
				if err != nil {
					fmt.Fprintln(stderr, "pw dev:", err)
					tailwind = nil
					tailwindExited = nil
				}
			}
			report.Phase("generating")
			if _, err := generateProject(ctx, false, stdout, false); err != nil {
				fmt.Fprintln(stderr, "pw dev:", err)
				report.Failed(err)
				state = next
				continue
			}
			report.Phase("building assets")
			if assets, err := buildDerivedAssets(root, config.Assets); err != nil {
				fmt.Fprintln(stderr, "pw dev:", err)
				report.Failed(err)
			} else {
				reportDerivedAssets(stdout, assets)
			}
			migrateErr := error(nil)
			if config.Migration.Auto {
				report.Phase("applying migrations")
				if version := changedMigrationVersion(root, config, state, next); version > 0 {
					migrateErr = reapplyDevMigrations(ctx, root, config, version, stdout, stderr)
				} else {
					migrateErr = runDevMigrations(ctx, root, config, stdout, stderr)
				}
			}
			if migrateErr != nil {
				fmt.Fprintln(stderr, "pw dev:", migrateErr)
				report.Failed(migrateErr)
				state = next
				continue
			}
			// The harness is rebuilt with the project, so a template edit is
			// visible in the storybook for the same reason it is visible in
			// the application.
			storybook.start(root, storybookStyles(config, readDevelopmentServer(root)), stdout, stderr)
			state, _ = watchSnapshot(root, config, tailwind == nil)
			report.Phase("building and starting the application")
			app, exited, err = startApplication(ctx, root, config.Main, idp, telemetry, logs, console, config.Console, attachToken, bootLog, stdout, stderr)
			if err != nil {
				fmt.Fprintln(stderr, "pw dev:", err)
				report.Failed(err)
			} else {
				report.Healthy()
			}
			report.Done()
			telemetry.monitor(app)
		}
	}
}

func startApplication(ctx context.Context, root, mainPackage string, idp *devIdentityProvider, telemetry *devTelemetryViewer, logs *devLogCapture, console *devconsole.Console, settings consoleConfig, attachToken string, bootLog *devBootLog, stdout, stderr io.Writer) (*exec.Cmd, <-chan error, error) {
	// Not CommandContext: its cancellation kills this process the moment the
	// interrupt arrives, and a kill is the one signal `go run` cannot pass down
	// to the binary it compiled. The loop stops the application through
	// stopCommand instead, which addresses the whole group and waits.
	command := exec.Command("go", "run", "-tags=pwdev", mainPackage)
	// The application's stderr goes through the reload report rather than
	// straight to the terminal, which is what makes it a pipe and why the boot
	// log has to be told what it can no longer see for itself.
	command.Dir, command.Stdout, command.Stderr, command.Stdin = root, stdout, bootLog, os.Stdin
	command.Env = bootLogEnviron(root, terminalWriter(stderr), logs.environ(consoleEnviron(console, settings, attachToken, telemetry.environ(idp.environ(developmentEnviron())))))
	ownProcessGroup(command)
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	result := make(chan error, 1)
	go func() { result <- command.Wait() }()
	return command, result, nil
}

// developmentEnviron runs the application under APP_ENV=dev unless the
// developer already selected another environment.
func developmentEnviron() []string {
	environ := os.Environ()
	if value, ok := os.LookupEnv(pwenv.Var); ok && strings.TrimSpace(value) != "" {
		return environ
	}
	return append(environ, pwenv.Var+"="+pwenv.Development)
}

// stopGrace is how long a child has to leave on its own before it is killed.
const stopGrace = 750 * time.Millisecond

// stopCommand ends a child process and everything it started, and returns only
// once they are gone. Both halves matter. The signal goes to the process group
// because the application is a grandchild of the `go run` pw holds, and the wait
// is what keeps the restart from binding a port the previous process has not
// released yet.
//
// exited is the channel the command's Wait result is published on. Without one
// there is nothing to wait for, so the caller gets the grace period and a kill.
func stopCommand(command *exec.Cmd, exited <-chan error) {
	if command == nil || command.Process == nil {
		return
	}
	if exited == nil {
		// No waiter goroutine exists, so nothing writes ProcessState
		// concurrently and the field answers "already gone" safely.
		if command.ProcessState != nil {
			return
		}
		_ = signalProcessGroup(command, os.Interrupt)
		time.Sleep(stopGrace)
		_ = signalProcessGroup(command, os.Kill)
		return
	}
	// With a channel the "already gone" answer is read off it rather than off
	// ProcessState, because that field is written by the Wait goroutine and
	// reading it here would race the write. The channel is drained without
	// blocking: a process stopped once already had its exit consumed, and the
	// loop stops the application twice — once on rebuild, once on the way out.
	select {
	case <-exited:
		return
	default:
	}
	if signalProcessGroup(command, os.Interrupt) != nil {
		// The group is already gone and its exit was reaped by an earlier
		// stop; there is nothing left to wait for.
		return
	}
	select {
	case <-exited:
		// It left on the interrupt. Killing it afterwards would be aimed at a
		// pid the operating system is free to have reused.
		return
	case <-time.After(stopGrace):
	}
	_ = signalProcessGroup(command, os.Kill)
	select {
	case <-exited:
	case <-time.After(stopGrace):
		// Bounded rather than open-ended: the exit may already have been
		// consumed between the drain above and the interrupt, and a wait for
		// a second send would be a wait forever.
	}
}

func startDevboxServices(ctx context.Context, root string, stdout, stderr io.Writer) func() {
	// A project that declined the Devbox environment manages its services
	// itself, so there is nothing here to start and nothing to report.
	if _, err := os.Stat(filepath.Join(root, "devbox.json")); os.IsNotExist(err) {
		return func() {}
	}
	if _, err := exec.LookPath("devbox"); err != nil {
		fmt.Fprintln(stderr, "pw dev: devbox is not installed; skipping configured services")
		return func() {}
	}
	if devboxReportsNoServices(ctx, root) {
		// A project with no database and no cache defines no service, which is
		// an ordinary shape rather than a misconfiguration. Starting them anyway
		// makes devbox print an error that reads like one.
		return func() {}
	}
	// process-compose opens a full-screen terminal UI by default, which paints
	// over the generate, migrate, identity provider, and application output the
	// developer loop prints. Disabling it keeps every service on the same log
	// stream as the rest of pw dev, one prefixed line per event.
	command := exec.Command("devbox", "services", "up", "--pcflags=-t=false")
	command.Dir, command.Stdout, command.Stderr, command.Env = root, stdout, stderr, os.Environ()
	ownProcessGroup(command)
	if err := command.Start(); err != nil {
		fmt.Fprintln(stderr, "pw dev: start Devbox services:", err)
		return func() {}
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	return func() {
		stopCommand(command, exited)
		// Interrupting devbox does not reach the process-compose it spawned, so
		// the services would outlive the developer loop that started them.
		stop := exec.Command("devbox", "services", "stop")
		stop.Dir, stop.Env = root, os.Environ()
		// Collected rather than streamed, because the one failure here that is
		// not a failure has to be recognised before it is printed. A loop that
		// ends before the services finish coming up — a generation error on the
		// first run is enough — has nothing to stop, and devbox says so with an
		// error of its own. Both that error and a line naming the shutdown would
		// land under the message that actually ended the run, and read like the
		// cause of it.
		output, err := stop.CombinedOutput()
		if err != nil && bytes.Contains(output, []byte("Process manager is not running")) {
			return
		}
		if len(output) > 0 {
			fmt.Fprint(stdout, string(output))
		}
		if err != nil {
			fmt.Fprintln(stderr, "pw dev: stop Devbox services:", err)
		}
	}
}

type watchState map[string]fileState

type fileState struct {
	size    int64
	modTime time.Time
}

// watchSnapshot records the current state of everything pw dev reacts to. The
// walk covers the module rather than the generation sources, because any Go
// source is a rebuild input; dev.watch.excludes is what keeps it cheap.
func watchSnapshot(root string, config projectConfig, tailwindStopped bool) (watchState, error) {
	extra := configuredWatchPaths(root, config.Watch.Includes,
		append(tailwindWatchPaths(root, config.Tailwind, tailwindStopped),
			migrationWatchPaths(root, config.Migration)...))
	return snapshotWatchFiles(root, config.Watch.Excludes, extra...)
}

func snapshotWatchFiles(root string, excludes []string, extra ...string) (watchState, error) {
	// The walk runs twice a second for the life of pw dev, so everything a
	// per-file test needs is computed here once: the walk hands back paths
	// joined under the cleaned root, which is what lets the loop compare and
	// look up without a Join, Rel, or Clean per file.
	root = filepath.Clean(root)
	state := watchState{}
	included := make(map[string]bool, len(extra))
	for _, path := range extra {
		if path != "" {
			included[filepath.Clean(path)] = true
		}
	}
	skipped := make(map[string]bool, len(excludes))
	for _, entry := range excludes {
		skipped[filepath.Clean(filepath.Join(root, filepath.FromSlash(entry)))] = true
	}
	distPath := filepath.Join(root, "dist")
	publicRoot := filepath.Join(root, "public")
	publicPrefix := publicRoot + string(filepath.Separator)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".pw", ".devbox", "vendor", "node_modules":
				return filepath.SkipDir
			}
			// dist holds what this loop produces, so watching it would make
			// every rebuild trigger the next one.
			if path == distPath || skipped[path] {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		// An authored asset is a build input now: editing one rebuilds the
		// served tree, and editing one a conversion reads also regenerates.
		if strings.HasPrefix(path, publicPrefix) {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			state[path] = fileState{size: info.Size(), modTime: info.ModTime()}
			return nil
		}
		if !included[path] && name != "popcornweb.toml" && !pwenv.IsFileName(name) && !pwenv.IsDotenvFileName(name) &&
			!strings.HasSuffix(name, ".go") &&
			!strings.HasSuffix(name, ".pw.html") && !strings.HasSuffix(name, ".pw.sql") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state[path] = fileState{size: info.Size(), modTime: info.ModTime()}
		return nil
	})
	return state, err
}

// migrationWatchPaths adds migration sources to the watch set. They are plain
// .sql files, which the default walk ignores.
func migrationWatchPaths(root string, config migrationConfig) []string {
	directory := config.Dir
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(root, filepath.FromSlash(directory))
	}
	matches, err := filepath.Glob(filepath.Join(directory, "*.sql"))
	if err != nil {
		return nil
	}
	return matches
}

// runDevMigrations applies pending migrations before the application starts. A
// project without a migration directory is not an error.
func runDevMigrations(ctx context.Context, root string, config projectConfig, stdout, stderr io.Writer) error {
	if !config.Migration.Auto {
		return nil
	}
	directory := config.Migration.Dir
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(root, filepath.FromSlash(directory))
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return nil
	}
	return executeMigrate(ctx, project{root: root, config: config}, migrateOptions{action: "up"}, stdout, stderr)
}

// reapplyDevMigrations brings the schema back in line with a migration file the
// developer just edited. A forward-only run does nothing useful here: the
// version is already recorded, so the one schema being worked on is the one the
// loop cannot reach. Rolling back to just before it and applying forward again
// is what makes editing a migration feel like editing anything else.
//
// This is the only automatic rollback in pw. It is bounded to the development
// loop, to a database whose schema the developer is editing, and it says what
// it is about to reverse: rows below that version are lost.
func reapplyDevMigrations(ctx context.Context, root string, config projectConfig, version int64, stdout, stderr io.Writer) error {
	target := project{root: root, config: config}
	fmt.Fprintf(stdout, "pw dev: migration %d changed; rolling back to %d and applying forward\n", version, version-1)
	// A migration with no usable Down stops before any statement runs, so the
	// schema is left alone rather than half-reversed.
	down := migrateOptions{action: "down-to", version: version - 1, confirm: true}
	if err := executeMigrate(ctx, target, down, stdout, stderr); err != nil {
		return err
	}
	if err := executeMigrate(ctx, target, migrateOptions{action: "up"}, stdout, stderr); err != nil {
		return err
	}
	if !config.Seed.Auto {
		return nil
	}
	// A project with no datasets has nothing to put back, which is an ordinary
	// shape rather than a misconfiguration to report on every edit.
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dbseed.DefaultDir))); err != nil || !info.IsDir() {
		return nil
	}
	// The cycle that emptied the database is the cycle that refills it. Seeding
	// is clear-insert, so it runs here and not on an ordinary rebuild, where it
	// would delete whatever the developer typed into the running application.
	if err := runSeed(ctx, nil, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "pw dev: reseed:", err)
	}
	return nil
}

// changedMigrationVersion reports the version of the lowest migration the
// developer has just edited or inserted below the ones already applied, or zero
// when the change is only a new highest version that a forward run handles.
func changedMigrationVersion(root string, config projectConfig, previous, next watchState) int64 {
	directory := config.Migration.Dir
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(root, filepath.FromSlash(directory))
	}
	var highestBefore, lowestChanged int64
	for path, before := range previous {
		if version, ok := migrationVersionOf(directory, path); ok {
			if version > highestBefore {
				highestBefore = version
			}
			if after, still := next[path]; still && after != before {
				if lowestChanged == 0 || version < lowestChanged {
					lowestChanged = version
				}
			}
		}
	}
	for path := range next {
		if _, existed := previous[path]; existed {
			continue
		}
		// A file that appears below the highest version the loop has already
		// applied is an insertion, and reaching it needs the same rollback an
		// edit does.
		if version, ok := migrationVersionOf(directory, path); ok && version <= highestBefore {
			if lowestChanged == 0 || version < lowestChanged {
				lowestChanged = version
			}
		}
	}
	return lowestChanged
}

// migrationVersionOf reads the numeric prefix of a migration file inside the
// migration directory, and reports false for anything else the walk saw.
func migrationVersionOf(directory, path string) (int64, bool) {
	if filepath.Dir(path) != directory || !strings.HasSuffix(path, ".sql") {
		return 0, false
	}
	name := filepath.Base(path)
	digits := name[:len(name)-len(strings.TrimLeft(name, "0123456789"))]
	if digits == "" {
		return 0, false
	}
	version, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	return version, true
}

func configuredWatchPaths(root string, patterns, additional []string) []string {
	paths := append([]string(nil), additional...)
	for _, pattern := range patterns {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(root, filepath.FromSlash(pattern))
		}
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			paths = append(paths, matches...)
			continue
		}
		paths = append(paths, filepath.Clean(pattern))
	}
	return paths
}

func tailwindWatchPaths(root string, config tailwindConfig, includeInput bool) []string {
	if !config.Enabled {
		return nil
	}
	var paths []string
	output := filepath.Clean(filepath.Join(root, filepath.FromSlash(config.Output)))
	public := filepath.Join(root, "public")
	if !pathWithin(public, output) {
		paths = append(paths, output)
	}
	if includeInput {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(config.Input)))
	}
	return paths
}

func pathWithin(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func addWatchFile(state watchState, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	state[filepath.Clean(path)] = fileState{size: info.Size(), modTime: info.ModTime()}
}

func equalWatchState(left, right watchState) bool {
	if len(left) != len(right) {
		return false
	}
	for path, value := range left {
		if right[path] != value {
			return false
		}
	}
	return true
}

// devboxReportsNoServices reports whether devbox said, in as many words, that
// this project defines no service.
//
// devbox exits zero whether or not it found one, so its own wording is the only
// signal on offer. The question is phrased negatively on purpose: an unreadable
// answer falls through to starting services as before, because skipping them
// for a project that does have a database would surface later as a connection
// failure that says nothing about why.
func devboxReportsNoServices(ctx context.Context, root string) bool {
	command := exec.CommandContext(ctx, "devbox", "services", "ls")
	command.Dir, command.Env = root, os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		return false
	}
	return bytes.Contains(output, []byte("No services found"))
}
