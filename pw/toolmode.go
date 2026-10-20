package pw

import "github.com/shibukawa/popcornweb/pwconfig"

// The command line is popcornweb/pwconfig's, and so is what the framework's
// own arguments mean. Both builds of one application therefore answer the same
// words: --generate-config, the health probe, and whatever subcommands the
// application registered.
//
// What each runtime keeps is when it answers them, which is a property of its
// own lifecycle rather than of the words.

func parseFrameworkAction(args []string) ([]string, error) {
	return pwconfig.ParseFrameworkAction(args)
}

func refusePendingFrameworkAction() error { return pwconfig.RefusePendingFrameworkAction() }

func runFrameworkAction() (bool, error) { return pwconfig.RunFrameworkAction() }

// RunFrameworkAction handles config generation and probes after ParseConfig.
// Call it before opening application resources; return when handled is true.
func RunFrameworkAction() (handled bool, err error) { return pwconfig.RunFrameworkAction() }
