//go:build tinygo || force_tinygo_logic

package pw

import (
	"compress/gzip"
	"io"
	"sync"
)

const gzipContentEncoding = "gzip"

// gzipResponseLevel is 1 here for the same reason as on the host, and since Go
// 1.27 the two builds agree on more than the level: the standard library runs
// the fast encoders it took from klauspost, so level 1 writes the same deflate
// bytes on both and only the header's modification time differs. Level 2 is no
// longer the cliff it was, but it buys 0.6 points of ratio for an eighth of the
// throughput, a trade the host declines too.
const gzipResponseLevel = 1

// The standard library encoder is used on this target rather than the
// klauspost one. Since Go 1.27 they are the same encoder, so klauspost would
// add a dependency to validate under TinyGo and write the same deflate bytes.
//
// Encoders are reset and reused, as on the host. Building one allocates about
// 800 KB, and on wasip1 that was most of what a response paid: a 22 KB body
// went through a new encoder at 52 MB/s and through a reused one at 199.
// TinyGo's sync.Pool never releases what it holds, so the price is that 800 KB
// staying resident per encoder, as many as the most responses ever compressed
// at once.
var responseGzipPool sync.Pool

type pooledResponseGzipEncoder struct {
	encoder  *gzip.Writer
	released bool
}

func newResponseGzipEncoder(w io.Writer) (responseEncoder, error) {
	if pooled := responseGzipPool.Get(); pooled != nil {
		encoder := pooled.(*gzip.Writer)
		encoder.Reset(w)
		return &pooledResponseGzipEncoder{encoder: encoder}, nil
	}
	encoder, err := gzip.NewWriterLevel(w, gzipResponseLevel)
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
// bytes nobody will read is worse than no trailer at all. The problem-response
// fallback is preserved exactly as the zstd encoder preserves it.
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
