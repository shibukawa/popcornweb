//go:build pwcloudflare

package pw

// hostRunsPerRequest is true under the pwcloudflare build tag, which pw build
// passes for a Cloudflare Workers artifact; requirement:cloudflare-process-state-refusal
// is checked at startup there.
const hostRunsPerRequest = true
