//go:build !pwcloudflare

package pw

// bootLogAutoOff says whether the automatic boot log format is off rather than
// a tree or a record. It is off only under a build tag naming a host that runs
// the program once per request, so an ordinary process is unaffected.
const bootLogAutoOff = false
