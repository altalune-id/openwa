package chat_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/chat"
)

func TestDisplayName_FallsBackToTheJIDUser(t *testing.T) {
	t.Parallel()
	require.Equal(t, "Toko Admin", (&chat.Chat{Name: "Toko Admin", JID: "1@g.us"}).DisplayName())
	require.Equal(t, "120363001", (&chat.Chat{JID: "120363001@g.us"}).DisplayName())
}
