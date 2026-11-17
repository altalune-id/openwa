package boot

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/user"
)

func TestUserStoreForAuth_CarriesDisplayPrefs(t *testing.T) {
	store := fakes.NewUser()
	u, err := user.New("prefs@example.com", "Prefs", user.SourceLocal)
	require.NoError(t, err)
	u.Theme, u.ColorMode, u.TipsSeen = "openwa", "dark", []string{"connect-number"}
	u.LastProjectID = uuid.MustParse("6f1c2c1e-0000-4000-8000-000000000002")
	require.NoError(t, store.Save(t.Context(), u))

	ref, err := userStoreForAuth{store: store}.ByEmail(t.Context(), u.Email)
	require.NoError(t, err)
	assert.Equal(t, "openwa", ref.Theme)
	assert.Equal(t, "dark", ref.ColorMode)
	assert.Equal(t, []string{"connect-number"}, ref.TipsSeen)
	assert.Equal(t, u.LastProjectID, ref.LastProjectID)
}
