package pw

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
)

// TestPooledEncodersLeaveNoTrace covers both pools on both builds. A frame
// aborted part way and a finished one must leave nothing in the encoder the
// next response takes, so a reused encoder writes exactly the bytes a new one
// writes, and releasing twice must not hand one encoder to two responses.
func TestPooledEncodersLeaveNoTrace(t *testing.T) {
	codings := []struct {
		name   string
		pool   *sync.Pool
		create func(io.Writer) (responseEncoder, error)
		decode func(*testing.T, []byte) string
		inner  func(responseEncoder) any
	}{
		{"gzip", &responseGzipPool, newResponseGzipEncoder, decodeGzip,
			func(e responseEncoder) any { return e.(*pooledResponseGzipEncoder).encoder }},
		{"zstd", &responseZstdPool, newResponseZstdEncoder, decodeZstd,
			func(e responseEncoder) any { return e.(*pooledResponseZstdEncoder).encoder }},
	}
	body := strings.Repeat("<li>pooled encoder</li>", 200)
	for _, coding := range codings {
		t.Run(coding.name, func(t *testing.T) {
			for coding.pool.Get() != nil {
			}
			encode := func(body string, abort bool) []byte {
				t.Helper()
				var out bytes.Buffer
				encoder, err := coding.create(&out)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := encoder.Write([]byte(body)); err != nil {
					t.Fatal(err)
				}
				if abort {
					encoder.Abort()
					encoder.Abort()
					return nil
				}
				if err := encoder.Close(); err != nil {
					t.Fatal(err)
				}
				if err := encoder.Close(); err != nil {
					t.Fatalf("a second Close = %v", err)
				}
				encoder.Abort()
				return out.Bytes()
			}

			fresh := encode(body, false)
			encode(strings.Repeat("<p>aborted before the commit</p>", 300), true)
			reused := encode(body, false)
			if !bytes.Equal(reused, fresh) {
				t.Fatal("a reused encoder wrote different bytes from a new one")
			}
			if got := coding.decode(t, reused); got != body {
				t.Fatalf("decoded %d bytes, want %d", len(got), len(body))
			}

			first, err := coding.create(io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			second, err := coding.create(io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if coding.inner(first) == coding.inner(second) {
				t.Fatal("one encoder was handed to two responses at once")
			}
			first.Abort()
			second.Abort()
		})
	}
}
