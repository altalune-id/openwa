package meow_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/internal/whatsapp"
	"altalune.id/openwa/internal/whatsapp/meow"
)

func newEngine(t *testing.T) *meow.Engine {
	t.Helper()
	h := pgtest.NewDatabase(t)
	db := h.OpenDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	require.NoError(t, meow.Upgrade(t.Context(), db, log))
	c, err := meow.NewContainer(db, log, "OpenWA")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return meow.NewEngine(c, meow.Options{InboundQueueSize: 8, Log: log})
}

func TestEngine_OpenOfAnUnknownJIDIsSessionGone(t *testing.T) {
	e := newEngine(t)
	_, err := e.Open(t.Context(), whatsapp.SessionRef{DeviceID: uuid.New(), JID: "628111:1@s.whatsapp.net"}, &fakes.Sink{})
	require.True(t, whatsapp.IsSessionGoneError(err), "got %v", err)
}

func TestEngine_PurgeIsIdempotent(t *testing.T) {
	e := newEngine(t)
	require.NoError(t, e.Purge(t.Context(), whatsapp.SessionRef{DeviceID: uuid.New()}))
	require.NoError(t, e.Purge(t.Context(), whatsapp.SessionRef{DeviceID: uuid.New(), JID: "628111:1@s.whatsapp.net"}))
}

func TestEngine_FreshOpenDoesNotConnectAndCloses(t *testing.T) {
	e := newEngine(t)
	require.Equal(t, whatsapp.EngineWhatsmeow, e.Name())
	require.True(t, e.Capabilities().QRLink)
	s, err := e.Open(t.Context(), whatsapp.SessionRef{DeviceID: uuid.New()}, &fakes.Sink{})
	require.NoError(t, err)
	require.False(t, s.Connected())
	require.NoError(t, s.Close(t.Context()))
}
