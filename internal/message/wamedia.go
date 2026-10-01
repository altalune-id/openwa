package message

import (
	"context"
	"io"
	"os"
)

// DefaultMediaConcurrency bounds how many WhatsApp media downloads run at once in this process.
const DefaultMediaConcurrency = 4

// WAMedia is the v1 MediaStore: every Open downloads the file from WhatsApp into an unlinked temp file.
type WAMedia struct {
	transport Transport
	sem       chan struct{}
	maxBytes  int64
}

var _ MediaStore = (*WAMedia)(nil)

// NewWAMedia builds the store; concurrency below 1 is treated as 1, and a file declared larger than maxBytes is refused.
func NewWAMedia(t Transport, concurrency int, maxBytes int64) *WAMedia {
	return &WAMedia{transport: t, sem: make(chan struct{}, max(concurrency, 1)), maxBytes: maxBytes}
}

// SECURITY: the declared size is checked before the semaphore and the fetched file size after, so a forged FileLength cannot fill the temp directory.
func (w *WAMedia) Open(ctx context.Context, m *Message) (*os.File, string, error) {
	if m.Media == nil || m.Media.Keys.DirectPath == "" {
		return nil, "", &MediaUnavailableError{ID: m.ID.String()}
	}
	if w.maxBytes > 0 && m.Media.Size > w.maxBytes {
		return nil, "", &MediaTooLargeError{Size: m.Media.Size, Max: w.maxBytes}
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	select {
	case w.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	defer func() { <-w.sem }()
	f, err := w.transport.FetchMedia(WithMediaLimit(ctx, w.maxBytes), m.DeviceID, m.Media.Keys, string(m.Type))
	if err != nil {
		return nil, "", err
	}
	if w.maxBytes > 0 {
		info, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return nil, "", statErr
		}
		if info.Size() > w.maxBytes {
			_ = f.Close()
			return nil, "", &MediaTooLargeError{Size: info.Size(), Max: w.maxBytes}
		}
	}
	return f, m.Media.Mime, nil
}

type mediaLimitKey struct{}

// WithMediaLimit tells a Transport adapter the most bytes it may write for one download; zero means unlimited.
func WithMediaLimit(ctx context.Context, n int64) context.Context {
	return context.WithValue(ctx, mediaLimitKey{}, n)
}

// MediaLimit returns the cap WithMediaLimit set, or zero.
func MediaLimit(ctx context.Context) int64 {
	n, _ := ctx.Value(mediaLimitKey{}).(int64)
	return n
}

// Put is a no-op in v1; an S3 store will upload here at receive time.
func (w *WAMedia) Put(context.Context, *Message, io.Reader) (string, error) { return "", nil }
