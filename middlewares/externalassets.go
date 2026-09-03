package middlewares

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/shibukawa/popcornweb/pwruntime"
)

// ExternalAsset is one object an ExternalAssetSource answered with: the whole
// body, so Range and conditional requests are served by http.ServeContent
// from memory, and the validators the store knows.
type ExternalAsset struct {
	Body      []byte
	ModTime   time.Time
	ETag      string
	MediaType string
}

// ExternalAssetSource answers the external public tree from somewhere other
// than the directory beside the process, per requirement:cloudflare-r2-storage:
// a host with no filesystem reads the tree from an object store. name is the
// manifest path, which is the key the build uploaded the file under. A name
// the source does not hold returns ok false and no error; an error is the
// store failing, which is reported and answered as 500 like an unreadable
// file.
type ExternalAssetSource interface {
	OpenExternalAsset(ctx context.Context, name string) (asset ExternalAsset, ok bool, err error)
}

var externalSourceState = struct {
	sync.RWMutex
	value ExternalAssetSource
}{}

// RegisterExternalAssetSource installs the store the external tree is read
// from. Nothing registered means the directory beside the process, which is
// every host but a Worker.
func RegisterExternalAssetSource(source ExternalAssetSource) {
	externalSourceState.Lock()
	defer externalSourceState.Unlock()
	externalSourceState.value = source
}

func registeredExternalSource() ExternalAssetSource {
	externalSourceState.RLock()
	defer externalSourceState.RUnlock()
	return externalSourceState.value
}

// serveExternalFromSource answers name from the registered source, having
// left every header the caller owns already set. It reports whether it
// answered: a name the source does not hold is the caller's 404 or 500 to
// decide, because the manifest-less path and the manifest path disagree on
// what an absent name means.
func serveExternalFromSource(w http.ResponseWriter, r *http.Request, source ExternalAssetSource, name string) (answered bool) {
	asset, ok, err := source.OpenExternalAsset(r.Context(), name)
	if err != nil {
		reportExternalSourceError(r.Context(), name, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return true
	}
	if !ok {
		return false
	}
	if asset.MediaType != "" && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", asset.MediaType)
	}
	if asset.ETag != "" {
		w.Header().Set("ETag", asset.ETag)
	}
	http.ServeContent(w, r, name, asset.ModTime, bytes.NewReader(asset.Body))
	return true
}

// reportedExternalSourceErrors keeps one failing key to one log line, as
// reportedMissingExternal does.
var reportedExternalSourceErrors sync.Map

func reportExternalSourceError(ctx context.Context, name string, err error) {
	if _, seen := reportedExternalSourceErrors.LoadOrStore(name, struct{}{}); seen {
		return
	}
	pwruntime.ReadLogger(ctx).Error("external public asset store failed",
		pwruntime.String("asset", name), pwruntime.String("error", err.Error()))
}

// ReadAll is io.ReadAll, exported for a source implementation that receives
// a stream and has to hand back the body http.ServeContent can seek in.
func ReadAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }
