package pw

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	tinybind "github.com/shibukawa/tinybind-go/httpbind"
)

func Parse[T any](r *http.Request) (T, error) {
	media, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	if !strings.EqualFold(strings.TrimSpace(media), "application/x-www-form-urlencoded") {
		return tinybind.Bind[T](r)
	}
	original := r.Body
	if original == nil {
		original = http.NoBody
	}
	if r.PostForm != nil {
		// CSRF or application code may have parsed the form already. New
		// generated binders read raw spans, so replay that parsed body for them.
		r.Body = &formBodyReader{Reader: strings.NewReader(r.PostForm.Encode()), Closer: original}
		return tinybind.Bind[T](r)
	}
	// Keep FormValue usable after either a successful bind or a validation
	// error. Capture only what the binder reads, without eagerly parsing a map.
	var saved formBodyCapture
	r.Body = &formBodyReader{Reader: io.TeeReader(original, &saved), Closer: original}
	defer func() {
		r.Body = &formBodyReader{Reader: io.MultiReader(bytes.NewReader(saved.Bytes()), original), Closer: original}
	}()
	return tinybind.Bind[T](r)
}

type formBodyReader struct {
	io.Reader
	io.Closer
}

type formBodyCapture struct{ buffer bytes.Buffer }

func (body *formBodyCapture) Len() int      { return body.buffer.Len() }
func (body *formBodyCapture) Bytes() []byte { return body.buffer.Bytes() }

func (body *formBodyCapture) Write(data []byte) (int, error) {
	// Match tinybind's 10 MiB form bound plus its overflow sentinel. Even a
	// custom binder reading farther cannot turn this replay into unbounded RAM.
	const limit = (10 << 20) + 1
	size := len(data)
	if room := limit - body.Len(); room > 0 {
		if len(data) > room {
			data = data[:room]
		}
		_, _ = body.buffer.Write(data)
	}
	return size, nil
}
