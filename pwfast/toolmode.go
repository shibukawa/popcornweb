package pwfast

import "github.com/shibukawa/popcornweb/pwconfig"

// RunFrameworkAction handles config generation and probes after ParseConfig.
// Call it before opening application resources; return when handled is true.
func RunFrameworkAction() (handled bool, err error) { return pwconfig.RunFrameworkAction() }
