package message_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
)

func TestNewRetentionPolicy_Bounds(t *testing.T) {
	for _, days := range []int{0, -1, 366} {
		_, err := message.NewRetentionPolicy(uuid.New(), uuid.New(), days)
		require.True(t, message.IsInvalidRetentionError(err), "%d", days)
	}
	p, err := message.NewRetentionPolicy(uuid.New(), uuid.New(), 7)
	require.NoError(t, err)
	now := time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 9, 21, 3, 0, 0, 0, time.UTC), p.Cutoff(now))
}
