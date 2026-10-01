package tag_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/blog/tag"
	"altalune.id/openwa/internal/platform/tenant"
)

func TestPostgres_Tag_TenantMissing(t *testing.T) {
	f := newPgFixture(t)
	bare := t.Context()

	tg, err := tag.New(f.tc.OrgID, f.tc.ProjectID, "x", "")
	require.NoError(t, err)

	assert.True(t, tenant.IsMissingError(f.store.Save(bare, tg)))
	_, err = f.store.ByID(bare, tg.ID)
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.BySlug(bare, f.tc.OrgID, f.tc.ProjectID, "x")
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.List(bare, f.tc.OrgID, f.tc.ProjectID)
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.ByIDs(bare, f.tc.OrgID, f.tc.ProjectID, []uuid.UUID{tg.ID})
	assert.True(t, tenant.IsMissingError(err))
	assert.True(t, tenant.IsMissingError(f.store.Delete(bare, tg.ID)))
}
