//go:build tinygo || force_tinygo_logic

package pw

import (
	"io"
	"sync"

	"github.com/shibukawa/tinygodriver/compress/zstd"
)

const zstdContentEncoding = zstd.ContentEncoding

// Encoders are reset and reused, as on the host. Reset keeps what the bounded
// encoder is made of, a 128 KiB block buffer and a 16 KiB match table, and the
// WithETag setting chosen here. On wasip1 a 22 KB body went through a new
// encoder at 45 MB/s and through a reused one at 56, and the 286 KB a new one
// allocates fell to 16 KB. As with gzip, TinyGo's sync.Pool never releases
// what it holds, so every encoder ever built stays resident.
var responseZstdPool sync.Pool

type pooledResponseZstdEncoder struct {
	encoder  *zstd.Writer
	released bool
}

func newResponseZstdEncoder(w io.Writer) (responseEncoder, error) {
	if pooled := responseZstdPool.Get(); pooled != nil {
		encoder := pooled.(*zstd.Writer)
		encoder.Reset(w)
		return &pooledResponseZstdEncoder{encoder: encoder}, nil
	}
	encoder, err := zstd.NewWriter(w, zstd.WithETag(false))
	if err != nil {
		return nil, err
	}
	return &pooledResponseZstdEncoder{encoder: encoder}, nil
}

func (z *pooledResponseZstdEncoder) Write(p []byte) (int, error) { return z.encoder.Write(p) }
func (z *pooledResponseZstdEncoder) Flush() error                { return z.encoder.Flush() }

func (z *pooledResponseZstdEncoder) Close() error {
	if z.released {
		return nil
	}
	err := z.encoder.Close()
	z.release()
	return err
}

// Abort returns the encoder without closing it. The aborted pre-commit frame is
// discarded rather than finished, which preserves the problem-response
// fallback, and Reset hands the next response a frame of its own.
func (z *pooledResponseZstdEncoder) Abort() {
	if z.released {
		return
	}
	z.release()
}

func (z *pooledResponseZstdEncoder) release() {
	z.encoder.Reset(io.Discard)
	z.released = true
	responseZstdPool.Put(z.encoder)
}
