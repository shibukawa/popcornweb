//go:build !tinygo && !force_tinygo_logic

package pw

import (
	"io"
	"sync"

	kgzip "github.com/klauspost/compress/gzip"
)

const gzipContentEncoding = "gzip"

// gzipResponseLevel is the fastest level that still compresses.
//
// A dynamic body is encoded while a request waits, so throughput is the scarce
// resource and ratio is the one that gives: level 1 runs at roughly 560 MB/s
// against 360 at level 6, for 3.6 points of ratio on a page-sized document.
// The deep levels are not lost, they are spent by the public asset build,
// where the cost lands on a machine that is not answering a request.
const gzipResponseLevel = 1

// The klauspost encoder is used here rather than the standard library one
// because it is still about an eighth faster at this level, now that Go 1.27
// runs the same encoder and writes the same deflate bytes, and the module is
// already linked for zstd, so the coding costs no new dependency.
var responseGzipPool sync.Pool

type pooledResponseGzipEncoder struct {
	encoder  *kgzip.Writer
	released bool
}

func newResponseGzipEncoder(w io.Writer) (responseEncoder, error) {
	if pooled := responseGzipPool.Get(); pooled != nil {
		encoder := pooled.(*kgzip.Writer)
		encoder.Reset(w)
		return &pooledResponseGzipEncoder{encoder: encoder}, nil
	}
	encoder, err := kgzip.NewWriterLevel(w, gzipResponseLevel)
	if err != nil {
		return nil, err
	}
	return &pooledResponseGzipEncoder{encoder: encoder}, nil
}

func (g *pooledResponseGzipEncoder) Write(p []byte) (int, error) { return g.encoder.Write(p) }
func (g *pooledResponseGzipEncoder) Flush() error                { return g.encoder.Flush() }

func (g *pooledResponseGzipEncoder) Close() error {
	if g.released {
		return nil
	}
	err := g.encoder.Close()
	g.release()
	return err
}

// Abort returns the encoder without closing it, because a trailer written for
// bytes nobody will read is worse than no trailer at all.
func (g *pooledResponseGzipEncoder) Abort() {
	if g.released {
		return
	}
	g.release()
}

func (g *pooledResponseGzipEncoder) release() {
	g.encoder.Reset(io.Discard)
	g.released = true
	responseGzipPool.Put(g.encoder)
}
