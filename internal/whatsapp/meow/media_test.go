package meow

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/whatsapp"
)

type fakeDownloader struct {
	body []byte
	err  error
	kind whatsmeow.MediaType
}

func (f *fakeDownloader) DownloadMediaWithPathToFile(_ context.Context, _ string, _, _, _ []byte, mt whatsmeow.MediaType, _ string, _ bool, file whatsmeow.File) error {
	f.kind = mt
	if f.err != nil {
		return f.err
	}
	_, err := file.Write(f.body)
	return err
}

func TestFetchToTemp_ReturnsAnUnlinkedRewoundFile(t *testing.T) {
	d := &fakeDownloader{body: []byte("decrypted")}
	f, err := fetchToTemp(t.Context(), d, whatsapp.MediaKeys{DirectPath: "/v/t62"}, whatsapp.KindImage, "dev")
	require.NoError(t, err)
	defer f.Close()
	require.Equal(t, whatsmeow.MediaImage, d.kind)
	_, statErr := os.Stat(f.Name())
	require.True(t, os.IsNotExist(statErr), "the path is gone while the handle stays readable")
	body, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, "decrypted", string(body))
}

func TestFetchToTemp_ExpiredMediaIsUnavailable(t *testing.T) {
	_, err := fetchToTemp(t.Context(), &fakeDownloader{err: whatsmeow.ErrMediaDownloadFailedWith410}, whatsapp.MediaKeys{DirectPath: "/v"}, whatsapp.KindVideo, "dev")
	require.True(t, whatsapp.IsMediaUnavailableError(err))
	_, err = fetchToTemp(t.Context(), &fakeDownloader{}, whatsapp.MediaKeys{DirectPath: "/v"}, whatsapp.KindText, "dev")
	require.True(t, whatsapp.IsUnsupportedError(err))
}

type streamDownloader struct {
	chunks   int
	chunk    int
	written  int64
	finalLen int
}

func (d *streamDownloader) DownloadMediaWithPathToFile(ctx context.Context, _ string, _, _, _ []byte, _ whatsmeow.MediaType, _ string, _ bool, file whatsmeow.File) error {
	buf := make([]byte, d.chunk)
	for range d.chunks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := file.Write(buf)
		d.written += int64(n)
		if err != nil {
			return err
		}
	}
	if d.finalLen > 0 {
		return file.(interface{ Truncate(int64) error }).Truncate(int64(d.finalLen))
	}
	return nil
}

func TestFetchToTemp_CapStopsTheDownloadAtTheLimit(t *testing.T) {
	ctx := message.WithMediaLimit(t.Context(), 100)
	d := &streamDownloader{chunks: 1000, chunk: 10}
	_, err := fetchToTemp(ctx, d, whatsapp.MediaKeys{DirectPath: "/v"}, whatsapp.KindVideo, "dev")
	require.True(t, message.IsMediaTooLargeError(err), "got %v", err)
	require.Equal(t, int64(100+1+cipherOverhead), d.written, "the file never grows past max + 1 + the cipher overhead")
}

func TestFetchToTemp_ADecryptedFileAboveTheLimitIsRefused(t *testing.T) {
	ctx := message.WithMediaLimit(t.Context(), 100)
	d := &streamDownloader{chunks: 12, chunk: 10}
	_, err := fetchToTemp(ctx, d, whatsapp.MediaKeys{DirectPath: "/v"}, whatsapp.KindImage, "dev")
	require.True(t, message.IsMediaTooLargeError(err), "120 bytes on disk is over the cap of 100, got %v", err)
}

func TestFetchToTemp_ExactlyTheLimitPassesEvenThroughTheCipherPadding(t *testing.T) {
	ctx := message.WithMediaLimit(t.Context(), 100)
	d := &streamDownloader{chunks: 12, chunk: 10, finalLen: 100}
	f, err := fetchToTemp(ctx, d, whatsapp.MediaKeys{DirectPath: "/v"}, whatsapp.KindImage, "dev")
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestFetchToTemp_NoLimitMeansUnlimited(t *testing.T) {
	d := &streamDownloader{chunks: 50, chunk: 1000}
	f, err := fetchToTemp(t.Context(), d, whatsapp.MediaKeys{DirectPath: "/v"}, whatsapp.KindImage, "dev")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.Equal(t, int64(50000), d.written)
}

func TestCappedFile_RetryRewindsTheBudget(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cap")
	require.NoError(t, err)
	defer f.Close()
	cf := &cappedFile{File: f, limit: 10, cancel: func() {}}
	_, err = cf.Write(make([]byte, 8))
	require.NoError(t, err)
	_, err = cf.Seek(0, io.SeekStart)
	require.NoError(t, err)
	_, err = cf.Write(make([]byte, 10))
	require.NoError(t, err, "a retried download restarts at offset 0 with the whole budget")
	n, err := cf.Write([]byte{1})
	require.ErrorIs(t, err, errMediaCap)
	require.Zero(t, n)
	_, err = cf.Write([]byte{1})
	require.ErrorIs(t, err, errMediaCap, "once exceeded, every later write fails")
}
