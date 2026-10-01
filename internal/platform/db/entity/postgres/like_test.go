package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
)

func TestLikePrefix_EscapesWildcards(t *testing.T) {
	require.Equal(t, `bud%`, pgent.LikePrefix("Bud"))
	require.Equal(t, `50\%\_off\\%`, pgent.LikePrefix(`50%_off\`))
}
