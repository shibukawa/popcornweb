//go:build !pwcloudflare

package pw

// hostRunsPerRequest is true only under the build tag naming a host that
// instantiates the program per request, so an ordinary process keeps every
// configuration it has today.
const hostRunsPerRequest = false
