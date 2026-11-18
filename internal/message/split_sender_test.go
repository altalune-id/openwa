package message_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
)

func TestSplitSender_DropsDeviceSuffixesAndSortsByServer(t *testing.T) {
	t.Parallel()
	pn, lid := message.SplitSender("123:4@lid", "6281111222333:12@s.whatsapp.net")
	require.Equal(t, "6281111222333@s.whatsapp.net", pn)
	require.Equal(t, "123@lid", lid)
	pn, lid = message.SplitSender("6281111222333@s.whatsapp.net", "")
	require.Equal(t, "6281111222333@s.whatsapp.net", pn)
	require.Empty(t, lid)
}
