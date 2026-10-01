package meow

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"

	"altalune.id/openwa/internal/whatsapp"
)

func TestMapError(t *testing.T) {
	cases := []struct {
		name      string
		in        error
		check     func(error) bool
		retryable bool
		reason    string
	}{
		{"not logged in", whatsmeow.ErrNotLoggedIn, whatsapp.IsNotConnectedError, true, ""},
		{"not connected", whatsmeow.ErrNotConnected, whatsapp.IsNotConnectedError, true, ""},
		{"ack timeout", whatsmeow.ErrMessageTimedOut, whatsapp.IsEngineError, true, "ack_timeout"},
		{"reachout timelock", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 463), whatsapp.IsEngineError, false, "reachout_timelock"},
		{"server 500", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 500), whatsapp.IsEngineError, true, "server_error_500"},
		{"server 400", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 400), whatsapp.IsEngineError, false, "server_error_400"},
		{"media 403", whatsmeow.ErrMediaDownloadFailedWith403, whatsapp.IsMediaUnavailableError, false, ""},
		{"media 404", whatsmeow.ErrMediaDownloadFailedWith404, whatsapp.IsMediaUnavailableError, false, ""},
		{"media 410", fmt.Errorf("wrapped: %w", whatsmeow.ErrMediaDownloadFailedWith410), whatsapp.IsMediaUnavailableError, false, ""},
		{"deadline", context.DeadlineExceeded, whatsapp.IsEngineError, true, "cancelled"},
		{"other", errors.New("boom"), whatsapp.IsEngineError, true, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError("send", "dev-1", tt.in)
			require.True(t, tt.check(got), "got %T %v", got, got)
			require.Equal(t, tt.retryable, whatsapp.IsRetryable(got))
			if tt.reason != "" {
				e, ok := errors.AsType[*whatsapp.EngineError](got)
				require.True(t, ok)
				require.Equal(t, tt.reason, e.Reason)
			}
		})
	}
}
