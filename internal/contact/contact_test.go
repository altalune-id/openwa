package contact_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/contact"
)

func TestDisplayName_FallsBackInOrder(t *testing.T) {
	c := contact.New(uuid.New(), uuid.New(), uuid.New(), "628111@s.whatsapp.net")
	require.Equal(t, "628111", c.DisplayName(), "JID user part last")
	c.Phone = "628111"
	require.Equal(t, "628111", c.DisplayName())
	c.BusinessName = "Toko Budi"
	require.Equal(t, "Toko Budi", c.DisplayName())
	c.PushName = "budi"
	require.Equal(t, "budi", c.DisplayName())
	c.Name = "Budi Santoso"
	require.Equal(t, "Budi Santoso", c.DisplayName())
}

func TestListOpts_WithDefaults(t *testing.T) {
	require.Equal(t, contact.DefaultListLimit, contact.ListOpts{}.WithDefaults().Limit)
	require.Equal(t, contact.MaxListLimit, contact.ListOpts{Limit: 999}.WithDefaults().Limit)
}
