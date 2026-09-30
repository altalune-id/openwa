package meow

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"

	"altalune.id/openwa/internal/whatsapp"
)

func TestMapQRItem(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   whatsmeow.QRChannelItem
		kind whatsapp.LinkEventKind
	}{
		{"code", whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@x", Timeout: 60 * time.Second}, whatsapp.LinkEventCode},
		{"success", whatsmeow.QRChannelSuccess, whatsapp.LinkEventSuccess},
		{"timeout", whatsmeow.QRChannelTimeout, whatsapp.LinkEventTimeout},
		{"passkey request", whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventPasskeyRequest}, whatsapp.LinkEventUnsupported},
		{"passkey confirmation", whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventPasskeyResponse}, whatsapp.LinkEventUnsupported},
		{"error", whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventError, Error: errors.New("pair failed")}, whatsapp.LinkEventError},
		{"client outdated", whatsmeow.QRChannelClientOutdated, whatsapp.LinkEventError},
		{"unexpected state", whatsmeow.QRChannelErrUnexpectedEvent, whatsapp.LinkEventError},
		{"scanned without multidevice", whatsmeow.QRChannelScannedWithoutMultidevice, whatsapp.LinkEventError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := mapQRItem(tc.in, now)
			require.Equal(t, tc.kind, ev.Kind)
			switch tc.kind {
			case whatsapp.LinkEventCode:
				require.Equal(t, "2@x", ev.Code)
				require.Equal(t, now.Add(60*time.Second), ev.Expires)
			case whatsapp.LinkEventUnsupported:
				require.True(t, whatsapp.IsUnsupportedError(ev.Err))
			case whatsapp.LinkEventError:
				require.Error(t, ev.Err)
			}
		})
	}
}
