package meow

import (
	"context"
	"errors"
	"io"
	"os"

	"go.mau.fi/whatsmeow"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/whatsapp"
)

// NOTE: WhatsApp media is AES-CBC plus a 10-byte MAC, so the file holds up to 16 + 10 bytes more than the plaintext until whatsmeow decrypts it in place.
const cipherOverhead = 32

var errMediaCap = errors.New("meow: download cap reached")

// NOTE: the part of *whatsmeow.Client that decrypts media into a file.
type mediaDownloader interface {
	DownloadMediaWithPathToFile(ctx context.Context, directPath string, encFileHash, fileHash, mediaKey []byte, mediaType whatsmeow.MediaType, mmsType string, allowNoHash bool, file whatsmeow.File) error
}

var _ mediaDownloader = (*whatsmeow.Client)(nil)

// NOTE: Write refuses to grow the file past limit and cancels the download, so a huge attachment never lands on disk; every other File method is the embedded *os.File's.
type cappedFile struct {
	*os.File
	limit    int64
	cancel   context.CancelFunc
	exceeded bool
}

func (c *cappedFile) Write(p []byte) (int, error) {
	if c.limit <= 0 {
		return c.File.Write(p)
	}
	if c.exceeded {
		return 0, errMediaCap
	}
	pos, err := c.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	room := c.limit - pos
	if int64(len(p)) <= room {
		return c.File.Write(p)
	}
	c.exceeded = true
	c.cancel()
	n := 0
	if room > 0 {
		n, _ = c.File.Write(p[:room])
	}
	return n, errMediaCap
}

// NOTE: whatsmeow checks the MAC and SHA over the whole file, so a download lands in a temp file; the path is unlinked at once and the handle is the only reference. message.MediaLimit(ctx) bounds it: limit+1 bytes of plaintext may be fetched, so an oversize file is refused without being stored.
func fetchToTemp(ctx context.Context, d mediaDownloader, keys whatsapp.MediaKeys, kind, deviceID string) (*os.File, error) {
	mt, ok := mediaTypeFor(kind)
	if !ok {
		return nil, &whatsapp.UnsupportedError{Feature: "media kind " + kind}
	}
	f, err := os.CreateTemp("", "openwa-media-*")
	if err != nil {
		return nil, &whatsapp.EngineError{Op: "download", Reason: "tempfile", Err: err}
	}
	if err := os.Remove(f.Name()); err != nil {
		_ = f.Close()
		return nil, &whatsapp.EngineError{Op: "download", Reason: "unlink", Err: err}
	}
	limit := message.MediaLimit(ctx)
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cf := &cappedFile{File: f, cancel: cancel}
	if limit > 0 {
		cf.limit = limit + 1 + cipherOverhead
	}
	if err := d.DownloadMediaWithPathToFile(dctx, keys.DirectPath, keys.FileEncSHA256, keys.FileSHA256, keys.MediaKey, mt, "", false, cf); err != nil {
		_ = f.Close()
		if cf.exceeded {
			return nil, &message.MediaTooLargeError{Size: limit + 1, Max: limit}
		}
		return nil, mapError("download", deviceID, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, &whatsapp.EngineError{Op: "download", Reason: "stat", Err: err}
	}
	if limit > 0 && info.Size() > limit {
		_ = f.Close()
		return nil, &message.MediaTooLargeError{Size: info.Size(), Max: limit}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, &whatsapp.EngineError{Op: "download", Reason: "rewind", Err: err}
	}
	return f, nil
}
