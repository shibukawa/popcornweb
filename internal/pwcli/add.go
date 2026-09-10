package pwcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// addUsage names every capability the catalog carries.
//
// It is built from capabilityOrder rather than written out, because a hand-kept
// list drifts silently: the two routers and the Devbox environment were all
// installable and absent from it, and a caller who mistypes learns from this
// line which capabilities exist. The tutorial installs one of the routers.
var addUsage = "usage: pw add [" + strings.Join(capabilityOrder, "|") + "|<module-path>]"

// addOptions holds every answer the wizard collects. Unlike api:cli-init there
// is no flag form: these answers edit a project that already exists, and the
// review screen is where that edit is approved.
type addOptions struct {
	Capability string
	// Engine names the database. It seeds the DSN, and decides the dialect of
	// the starter schema and the development server package.
	Engine string
	// DSN overrides the engine default. Empty takes the engine's own, so the
	// answer follows the engine step rather than a value seeded before it.
	DSN string
	// AuthMode is the pw init authentication answer the auth capability
	// installs: authOIDC or authOAuth. Empty means authOIDC, so a caller that
	// predates the question keeps the mode it always got.
	AuthMode string
	// AuthEmulator selects requirement:contrib-devidp over an external provider.
	AuthEmulator bool
}

// authMode resolves the login mode the auth capability writes.
func (o addOptions) authMode() string {
	if o.AuthMode == "" {
		return authOIDC
	}
	return o.AuthMode
}

// databaseDSN resolves the DSN a plan writes for this project.
func (o addOptions) databaseDSN(project string) string {
	if o.DSN != "" {
		return o.DSN
	}
	return engineFor(o.Engine).DSN(project)
}

func runAdd(ctx context.Context, args []string, stdout io.Writer) error {
	root, err := projectRoot(".")
	if err != nil {
		return err
	}
	// A module path takes the package route, which writes a declaration and
	// copies nothing. A capability name takes the wizard below, which writes
	// files into the project and therefore still has something to review.
	for _, arg := range args {
		if isModulePath(arg) {
			if len(args) != 1 {
				return fmt.Errorf("add: name one module; %s", addUsage)
			}
			return addPackage(ctx, root, arg, stdout)
		}
	}
	state, err := loadProjectState(root)
	if err != nil {
		return err
	}
	missing, err := state.missingCapabilities()
	if err != nil {
		return err
	}
	options := addOptions{Engine: engineSQLite}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("add: %s takes no options; %s", arg, addUsage)
		}
		if options.Capability != "" {
			return errors.New(addUsage)
		}
		if !slices.Contains(capabilityOrder, arg) {
			return fmt.Errorf("add: unknown capability %q; %s", arg, addUsage)
		}
		options.Capability = arg
	}
	if options.Capability != "" {
		if evidence, present, err := state.carries(options.Capability); err != nil {
			return err
		} else if present {
			return fmt.Errorf("add: this project already has %s, per %s", options.Capability, evidence)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintln(stdout, "This project already has every capability pw add installs.")
		return nil
	}
	if !interactiveTerminal() {
		return fmt.Errorf("add: the wizard needs a terminal; %s", addUsage)
	}
	options, err = runAddWizard(state, missing, options)
	if errors.Is(err, errWizardCanceled) {
		fmt.Fprintln(stdout, "add canceled")
		return nil
	}
	if err != nil {
		return err
	}
	return applyCapability(ctx, state, options, stdout)
}

// applyCapability installs the answered capability, and the one it depends on
// when the project lacks that too.
func applyCapability(ctx context.Context, state projectState, options addOptions, stdout io.Writer) error {
	for _, name := range capabilityChain(state, options.Capability) {
		step := options
		step.Capability = name
		plan, err := planCapability(state, step)
		if err != nil {
			return err
		}
		if err := plan.apply(state.root); err != nil {
			return err
		}
		for _, line := range plan.summary() {
			fmt.Fprintln(stdout, " ", line)
		}
		if plan.generate {
			if _, err := generateProject(ctx, false, stdout, false); err != nil {
				return fmt.Errorf("generate %s: %w", name, err)
			}
		}
		// The next capability plans against what this one just wrote.
		refreshed, err := loadProjectState(state.root)
		if err != nil {
			return err
		}
		state = refreshed
	}
	fmt.Fprintf(stdout, "\nAdded %s\n", options.Capability)
	return nil
}

// capabilityChain orders a capability behind the one it requires. A required
// capability the project already has is not installed twice.
func capabilityChain(state projectState, name string) []string {
	required, ok := capabilityRequires[name]
	if !ok {
		return []string{name}
	}
	if _, present, err := state.carries(required); err != nil || present {
		return []string{name}
	}
	return []string{required, name}
}

// runAddWizard asks the capability-specific questions and reviews the files.
func runAddWizard(state projectState, missing []string, defaults addOptions, programOptions ...tea.ProgramOption) (addOptions, error) {
	return runWizard(newAddWizard(state, missing, defaults), programOptions...)
}

func newAddWizard(state projectState, missing []string, defaults addOptions) wizardModel[addOptions] {
	return wizardModel[addOptions]{
		steps:    addWizardSteps(state, missing, defaults),
		defaults: defaults,
		title:    "Popcorn Web  add capability",
		confirm:  "add",
		plan:     func(options addOptions) []string { return addReview(state, options) },
		theme:    newWizardTheme(),
	}
}

// addReview renders what the answers would do to the project, so the operator
// approves the effect rather than only the answers that produced it.
func addReview(state projectState, options addOptions) []string {
	var lines []string
	for _, name := range capabilityChain(state, options.Capability) {
		step := options
		step.Capability = name
		plan, err := planCapability(state, step)
		if err != nil {
			return []string{"cannot plan " + name + ": " + err.Error()}
		}
		if len(capabilityChain(state, options.Capability)) > 1 {
			lines = append(lines, name+":")
		}
		lines = append(lines, plan.summary()...)
		// Only the first capability can be planned against the current tree;
		// what follows is planned as if it were installed alone.
		break
	}
	if required, ok := capabilityRequires[options.Capability]; ok {
		if _, present, err := state.carries(required); err == nil && !present {
			lines = append(lines, "", options.Capability+" needs "+required+", which is added first")
		}
	}
	return lines
}

// addWizardSteps builds the question list. A capability adds one entry here,
// one probe, and one plan.
func addWizardSteps(state projectState, missing []string, defaults addOptions) []wizardStep[addOptions] {
	choices := make([]wizardChoice[addOptions], 0, len(missing))
	for _, name := range missing {
		description := capabilitySummary[name]
		if required, ok := capabilityRequires[name]; ok {
			if _, present, err := state.carries(required); err == nil && !present {
				description += "; adds " + required + " first"
			}
		}
		choices = append(choices, wizardChoice[addOptions]{
			name:        name,
			description: description,
			apply:       setCapability(name),
		})
	}
	steps := []wizardStep[addOptions]{
		newChoiceStep(
			"Capability",
			"Only what this project does not already carry is offered.",
			max(slices.Index(missing, defaults.Capability), 0),
			choices...,
		),
		when(func(options addOptions) bool { return needsDatabase(state, options) },
			newChoiceStep(
				"Database engine",
				"Decides the DSN, the dialect of the starter schema, and the development server. "+
					"An engine the project already uses is the one to pick.",
				engineCursor(defaults.Engine),
				addEngineChoices()...,
			),
		),
		when(func(options addOptions) bool { return needsDatabase(state, options) },
			newTextStep(
				"Database DSN",
				"pw dev and pw migrate open this connection. Leave it empty to take the engine default.",
				defaults.DSN,
				"empty for the engine default",
				validateDSN,
				func(target *addOptions, value string) { target.DSN = value },
			),
		),
		// The protocol is asked before the provider, because only one of the
		// two answers has an emulator to offer. A passkey mode stays a pw init
		// answer: it needs a relying-party registration bound to the origin
		// this deployment is reached on, which pw add cannot know.
		when(func(options addOptions) bool { return options.Capability == capabilityAuth },
			newChoiceStep(
				"Login",
				"Which protocol the provider speaks. The framework writes the matching [auth] section; handlers stay yours.",
				addAuthCursor(defaults.authMode()),
				wizardChoice[addOptions]{
					name:        "OIDC",
					description: "log in against an OpenID Provider",
					apply:       func(target *addOptions) { target.AuthMode = authOIDC },
				},
				wizardChoice[addOptions]{
					name:        "OAuth provider",
					description: "log in through a provider that issues no ID Token, such as X; no local emulator",
					apply:       func(target *addOptions) { target.AuthMode = authOAuth; target.AuthEmulator = false },
				},
			),
		),
		when(func(options addOptions) bool {
			return options.Capability == capabilityAuth && usesOIDC(options.authMode())
		},
			newChoiceStep(
				"OIDC provider",
				"The local emulator signs you in by picking a user from a list, so login works before a real IdP exists.",
				yesNoCursor(defaults.AuthEmulator),
				wizardChoice[addOptions]{
					name:        "Local emulator",
					description: "pw dev runs it and injects the issuer and client credentials",
					apply:       func(target *addOptions) { target.AuthEmulator = true },
				},
				wizardChoice[addOptions]{
					name:        "External provider",
					description: "fill in auth.oidc yourself, or supply it through the environment",
					apply:       func(target *addOptions) { target.AuthEmulator = false },
				},
			),
		),
	}
	return steps
}

// needsDatabase reports whether the answers reach the database capability,
// either directly or through the dependency of another one.
func needsDatabase(state projectState, options addOptions) bool {
	return slices.Contains(capabilityChain(state, options.Capability), capabilityDatabase)
}

func setCapability(name string) func(*addOptions) {
	return func(target *addOptions) { target.Capability = name }
}

// addEngineChoices renders the engine table as wizard choices, in catalog order.
func addEngineChoices() []wizardChoice[addOptions] {
	choices := make([]wizardChoice[addOptions], 0, len(engineOrder))
	for _, name := range engineOrder {
		engine := databaseEngines[name]
		choices = append(choices, wizardChoice[addOptions]{
			name:        engine.Label,
			description: engine.Summary,
			apply:       func(target *addOptions) { target.Engine = name },
		})
	}
	return choices
}

// validateDSN rejects what api:rdb-middleware could not open anyway. An empty
// value is the engine default, not a missing answer.
func validateDSN(value string) error {
	if value == "" {
		return nil
	}
	if !strings.Contains(value, "://") {
		return errors.New("a DSN looks like sqlite://app.db")
	}
	return nil
}

// addAuthCursor maps the login answer onto its position in the choice list.
func addAuthCursor(mode string) int {
	if mode == authOAuth {
		return 1
	}
	return 0
}
