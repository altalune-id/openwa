package meow

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"

	"altalune.id/openwa/internal/whatsapp"
)

func TestTranslate(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	other := errors.New("boom")
	require.NoError(t, translate(id, "op", nil))
	require.True(t, whatsapp.IsNotConnectedError(translate(id, "op", whatsmeow.ErrNotConnected)))
	require.True(t, whatsapp.IsNotConnectedError(translate(id, "op", whatsmeow.ErrNotLoggedIn)))
	require.True(t, whatsapp.IsInvalidPhoneError(translate(id, "op", whatsmeow.ErrPhoneNumberTooShort)))
	require.True(t, whatsapp.IsInvalidPhoneError(translate(id, "op", whatsmeow.ErrPhoneNumberIsNotInternational)))
	require.True(t, whatsapp.IsAlreadyLinkedError(translate(id, "op", whatsmeow.ErrQRStoreContainsID)))
	require.True(t, whatsapp.IsAlreadyLinkedError(translate(id, "op", whatsmeow.ErrQRAlreadyConnected)))
	err := translate(id, "op", other)
	require.True(t, whatsapp.IsEngineError(err))
	require.ErrorIs(t, err, other)
}
