package pwcli

import (
	"path/filepath"

	"github.com/shibukawa/popcornweb/internal/pwlsp"
)

// loadLSPProject is the requirement:pw-language-server project model, read
// through the same data:project-config loader api:cli-generate runs.
//
// The mapping it does is the whole of its job: decision:explicit-generation-sources
// says a directory is invisible to a purpose until popcornweb.toml lists it, so
// the server is told exactly which directories carry which dialect rather than
// being left to walk the tree and find sources generation would ignore.
func loadLSPProject(start string) (*pwlsp.Project, error) {
	root, err := projectRoot(start)
	if err != nil {
		// Every failure to find one is the same answer to the server: there is
		// no project here. Distinguishing an unreadable parent from an absent
		// file would give the developer a message about the walk rather than
		// about their workspace.
		return nil, pwlsp.ErrNoProject
	}
	config, err := loadProjectConfig(root)
	if err != nil {
		return nil, err
	}

	project := &pwlsp.Project{
		Root:             root,
		RouteTable:       config.Assets.locations().routes,
		ConfigPath:       filepath.Join(root, "popcornweb.toml"),
		Name:             config.Name,
		ConsolePort:      config.Console.Port,
		StorybookEnabled: config.Console.Enabled && config.Console.Storybook,
	}
	for _, purpose := range generatePurposes {
		for _, directory := range *purpose.target(&config.Generate) {
			project.Sources = append(project.Sources, pwlsp.SourceDir{
				Purpose: purpose.key,
				Dir:     filepath.Join(root, filepath.FromSlash(directory)),
				Kinds:   pwlsp.DialectsFor(purpose.key),
			})
		}
	}
	return project, nil
}
