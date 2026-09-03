package pw

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shibukawa/popcornweb/database"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/popcornweb/sessionconfig"
)

// workerRDBSchemes are the DSN schemes a Worker can reach. The host owns no
// socket and no filesystem, so a file engine and a TCP engine are both out;
// what remains is what a binding provides, per requirement:cloudflare-d1-engine.
var workerRDBSchemes = map[string]bool{"d1": true}

// refuseWorkerProcessState is requirement:cloudflare-process-state-refusal:
// under a host that instantiates the program per request, state kept in the
// process is empty on every request, and a configuration that keeps it there
// works and lies. Every offending key is named at once, with what to use
// instead, so the answer is an edit rather than a search.
func refuseWorkerProcessState(cache pwruntime.CacheConfig, session SessionConfig, ratelimit RateLimitConfig, rdb RDBConfig, storage pwruntime.StorageConfig) error {
	var refusals []string
	if cache.Enabled {
		refusals = append(refusals, "cache.enabled = true: a memo store lives in the process, which a Worker recreates per request, so every lookup would miss; set it false until a KV store exists")
	}
	if session.Enabled {
		switch session.Backend {
		case sessionconfig.SessionBackendDevVolatile, sessionconfig.SessionBackendDevPersist:
			refusals = append(refusals, fmt.Sprintf("session.backend = %q: a development store does not survive a Worker request; use %q, or %q over a d1 connection", session.Backend, sessionconfig.SessionBackendCookie, sessionconfig.SessionBackendRDB))
		}
	}
	if ratelimit.Enabled && ratelimit.Backend == "memory" {
		refusals = append(refusals, "ratelimit.backend = \"memory\": a per-process counter limits nothing on a host that runs the process per request; disable the limiter until a KV backend exists")
	}
	if rdb.Enabled {
		for _, connection := range rdb.Connections {
			scheme, _, err := database.Scheme(connection.DSN)
			if err != nil {
				// The pool code reports a malformed DSN in its own words.
				continue
			}
			if !workerRDBSchemes[scheme] {
				refusals = append(refusals, fmt.Sprintf("middleware.rdb.connections[%s]: a %s:// connection needs a file or a socket, and a Worker has neither; only a d1:// binding is reachable", connection.Group, scheme))
			}
		}
	}
	if storage.Enabled {
		for _, bucket := range storage.Buckets {
			if bucket.Backend != pwruntime.StorageBackendR2 {
				refusals = append(refusals, fmt.Sprintf("storage.buckets[%s]: the %s backend needs a directory or a socket, and a Worker has neither; use the r2 backend over a bucket binding", bucket.Name, bucket.Backend))
			}
		}
	}
	if len(refusals) == 0 {
		return nil
	}
	return errors.New("popcornweb: this configuration keeps state the Cloudflare Workers host cannot hold:\n  " + strings.Join(refusals, "\n  "))
}
