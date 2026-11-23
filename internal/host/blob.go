package host

import (
	"context"
	"fmt"

	"loom/internal/flow"
)

// BlobStore is the subset of the store a BlobHost needs.
type BlobStore interface {
	SaveBlob(data []byte) (string, error)
	LoadBlob(hash string) ([]byte, error)
}

// BlobHost grants a host access to the content-addressed blob store so
// nodes can spill large payloads into it (flow.BlobSaver) and read blob
// references back (flow.BlobLoader). Wrap the live host with it when the
// store is available; hermetic hosts implement the capabilities themselves
// without persistence.
type BlobHost struct {
	flow.Host
	blobs BlobStore
}

func NewBlobHost(base flow.Host, blobs BlobStore) *BlobHost {
	return &BlobHost{Host: base, blobs: blobs}
}

func (b *BlobHost) SaveBlob(data []byte) (string, int, error) {
	hash, err := b.blobs.SaveBlob(data)
	return hash, len(data), err
}

func (b *BlobHost) LoadBlob(hash string) ([]byte, error) {
	return b.blobs.LoadBlob(hash)
}

// Exec forwards the optional subprocess capability so nodes see the
// wrapped live host (the recorder tapes it like any other effect).
func (b *BlobHost) Exec(ctx context.Context, req flow.ExecRequest) (flow.ExecResult, error) {
	executor, ok := b.Host.(flow.Executor)
	if !ok {
		return flow.ExecResult{}, fmt.Errorf("host cannot execute subprocesses")
	}
	return executor.Exec(ctx, req)
}

// Close releases resources owned by the wrapped host, currently the bounded
// PostgreSQL connection cache.
func (b *BlobHost) Close() error {
	if closer, ok := b.Host.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
