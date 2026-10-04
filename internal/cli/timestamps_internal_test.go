package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/invite"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/project"
)

func TestListPayloads_PrintUTCTimestamps(t *testing.T) {
	at := time.Date(2026, 10, 2, 5, 30, 0, 0, time.FixedZone("WIB", 7*3600))
	want := "2026-10-01T22:30:00Z"

	require.Equal(t, want, orgToMap(&org.Org{CreatedAt: at})["created_at"])
	require.Equal(t, want, projectToMap(&project.Project{CreatedAt: at})["created_at"])
	inv := inviteToMap(&invite.Invite{ExpiresAt: at, CreatedAt: at})
	require.Equal(t, want, inv["expires_at"])
	require.Equal(t, want, inv["created_at"])
	require.Equal(t, "2026-10-01", tableDay(at))
}

func TestTableDateTimes_PrintUTC(t *testing.T) {
	at := time.Date(2026, 10, 2, 5, 30, 7, 0, time.FixedZone("WIB", 7*3600))

	require.Equal(t, "2026-10-01 22:30", tableDateTime(at))
	require.Equal(t, "2026-10-01 22:30:07", tableDateTimeSeconds(at))
}
