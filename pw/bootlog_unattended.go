//go:build pwcloudflare

package pw

// bootLogAutoOff is true under the pwcloudflare build tag, which pw build
// passes for a Cloudflare Workers artifact. That host instantiates the module
// and runs main for every request, so the once-per-process startup summary
// would be one record per request: noise rather than a summary. An explicit
// observability.boot_log still selects a format.
const bootLogAutoOff = true
