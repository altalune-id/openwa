package message_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/testutil/fakes"
)

func mediaRow() *message.Message {
	return &message.Message{ID: uuid.New(), DeviceID: uuid.New(), Type: message.TypeImage,
		Media: &message.Media{Mime: "image/png", Keys: message.MediaKeys{DirectPath: "/v/t62"}}}
}

func TestWAMedia_NoKeysIsUnavailable(t *testing.T) {
	w := message.NewWAMedia(&fakes.Transport{}, 4, 1<<20)
	_, _, err := w.Open(t.Context(), &message.Message{ID: uuid.New(), Media: &message.Media{}})
	require.True(t, message.IsMediaUnavailableError(err))
	_, _, err = w.Open(t.Context(), &message.Message{ID: uuid.New()})
	require.True(t, message.IsMediaUnavailableError(err))
}

func TestWAMedia_RefusesADeclaredSizeOverTheCapBeforeFetching(t *testing.T) {
	tr := &fakes.Transport{}
	w := message.NewWAMedia(tr, 4, 10)
	big := mediaRow()
	big.Media.Size = 11
	_, _, err := w.Open(t.Context(), big)
	require.True(t, message.IsMediaTooLargeError(err), "got %v", err)
	require.Zero(t, tr.Fetches, "nothing was downloaded")
}

func TestWAMedia_BoundsConcurrentFetches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var inFlight, peak atomic.Int32
		release := make(chan struct{})
		tr := &fakes.Transport{FetchFn: func(ctx context.Context, _ uuid.UUID, _ message.MediaKeys, _ string) (*os.File, error) {
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-release
			inFlight.Add(-1)
			return os.CreateTemp(t.TempDir(), "m")
		}}
		w := message.NewWAMedia(tr, 4, 1<<20)
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() {
				f, _, err := w.Open(t.Context(), mediaRow())
				if err == nil {
					_ = f.Close()
				}
			})
		}
		synctest.Wait()
		require.Equal(t, int32(4), inFlight.Load(), "two callers wait on the semaphore")
		close(release)
		wg.Wait()
		require.Equal(t, int32(4), peak.Load())
	})
}

func TestWAMedia_WaitRespectsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		block := make(chan struct{})
		tr := &fakes.Transport{FetchFn: func(context.Context, uuid.UUID, message.MediaKeys, string) (*os.File, error) {
			<-block
			return nil, os.ErrClosed
		}}
		w := message.NewWAMedia(tr, 1, 1<<20)
		go func() { _, _, _ = w.Open(context.Background(), mediaRow()) }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err := w.Open(ctx, mediaRow())
		require.ErrorIs(t, err, context.Canceled)
		close(block)
	})
}

func TestWAMedia_RefusesAnOversizedStreamDespiteASmallDeclaredSize(t *testing.T) {
	var got *os.File
	tr := &fakes.Transport{FetchFn: func(ctx context.Context, _ uuid.UUID, _ message.MediaKeys, _ string) (*os.File, error) {
		require.Equal(t, int64(10), message.MediaLimit(ctx), "the adapter is told the cap so it can stop the download")
		f, err := os.CreateTemp(t.TempDir(), "m")
		require.NoError(t, err)
		_, err = f.Write(make([]byte, 64))
		require.NoError(t, err)
		_, err = f.Seek(0, 0)
		require.NoError(t, err)
		got = f
		return f, nil
	}}
	w := message.NewWAMedia(tr, 4, 10)
	row := mediaRow()
	row.Media.Size = 1
	f, _, err := w.Open(t.Context(), row)
	require.Nil(t, f)
	require.True(t, message.IsMediaTooLargeError(err), "got %v", err)
	_, statErr := got.Stat()
	require.Error(t, statErr, "the oversized file was closed")
}

func TestWAMedia_AcceptsAStreamAtTheCap(t *testing.T) {
	tr := &fakes.Transport{FetchBody: make([]byte, 10)}
	w := message.NewWAMedia(tr, 4, 10)
	f, _, err := w.Open(t.Context(), mediaRow())
	require.NoError(t, err)
	_ = f.Close()
}
