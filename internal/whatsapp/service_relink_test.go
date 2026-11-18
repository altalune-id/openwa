package whatsapp_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/whatsapp"
)

func TestService_LinkAfterWhatsAppSignedTheNumberOutStartsLinking(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateConnected, f.ref.JID)
	f.svc.OnState(f.ref, whatsapp.StateLoggedOut, "logged_out_by_phone")
	row, _ := f.store.Row(f.ref.DeviceID)
	require.Equal(t, whatsapp.StateLoggedOut, row.State)
	require.Empty(t, row.JID, "a sign-out clears the identity")

	st, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)
	require.NoError(t, err, "Fix it on a signed-out number links again")
	require.Equal(t, whatsapp.OutcomePending, st.Outcome)
	require.Len(t, f.rt.Links, 1)
	row, _ = f.store.Row(f.ref.DeviceID)
	require.Equal(t, whatsapp.StateLinking, row.State)
}
