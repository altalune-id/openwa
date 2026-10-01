package category_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/blog/category"
	"altalune.id/openwa/internal/platform/tenant"
)

func TestPostgres_Category_TenantMissing(t *testing.T) {
	f := newPgFixture(t)
	bare := t.Context()

	c, err := category.New(f.tc.OrgID, f.tc.ProjectID, "Unscoped", "")
	require.NoError(t, err)

	assert.True(t, tenant.IsMissingError(f.store.Save(bare, c)))
	_, err = f.store.ByID(bare, c.ID)
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.List(bare, f.tc.OrgID, f.tc.ProjectID)
	assert.True(t, tenant.IsMissingError(err))
	assert.True(t, tenant.IsMissingError(f.store.Delete(bare, c.ID)))
}
