package blog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/blog"
	"altalune.id/openwa/internal/platform/tenant"
)

func TestPostgres_Post_TenantMissing(t *testing.T) {
	f := newPgFixture(t)
	bare := t.Context()

	p, err := blog.New(f.tc.OrgID, f.tc.ProjectID, f.cat, "X", "", "body")
	require.NoError(t, err)

	assert.True(t, tenant.IsMissingError(f.store.Save(bare, p, 0)))
	_, err = f.store.ByID(bare, p.ID)
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.List(bare, f.tc.OrgID, f.tc.ProjectID, blog.ListOpts{})
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.CountByCategory(bare, f.tc.OrgID, f.tc.ProjectID)
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.CountByTag(bare, f.tc.OrgID, f.tc.ProjectID)
	assert.True(t, tenant.IsMissingError(err))
	assert.True(t, tenant.IsMissingError(f.store.Delete(bare, p.ID, 0)))
}
