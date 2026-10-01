package contact_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/platform/tenant"
)

// SECURITY: the fixture role bypasses RLS, so these prove the store's own org predicate.
func TestPostgres_Contact_OtherOrgIsInvisibleWithoutRLS(t *testing.T) {
	f := newPgFixture(t)
	f.upsert(t, "628111@s.whatsapp.net", func(c *contact.Contact) { c.Name = "Victim" })
	victim, err := f.store.ByJID(f.ctx(t), f.device, "628111@s.whatsapp.net")
	require.NoError(t, err)

	attackerTC := f.seed.Tenant(t)
	attacker := tenant.Into(t.Context(), attackerTC)
	_, err = f.store.ByJID(attacker, victim.DeviceID, victim.JID)
	require.True(t, contact.IsNotFoundError(err), "another org never reads the row, even with its exact device and jid")

	hijack := contact.New(attackerTC.OrgID, attackerTC.ProjectID, f.device, "628111@s.whatsapp.net")
	hijack.Name = "Hijacked"
	err = f.store.Upsert(attacker, hijack)
	require.True(t, contact.IsNotFoundError(err), "the conflict clause must refuse another org's row, got %v", err)
	got, err := f.store.ByJID(f.ctx(t), f.device, "628111@s.whatsapp.net")
	require.NoError(t, err)
	require.Equal(t, "Victim", got.Name)
}

func (f pgFixture) siblingProject(t *testing.T) tenant.Context {
	t.Helper()
	id := uuid.New()
	_, err := f.seed.SQL.ExecContext(t.Context(),
		"INSERT INTO "+f.seed.Prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'Sibling', $4, now(), now())",
		id, f.tc.OrgID, id.String()[:8], f.tc.UserID)
	require.NoError(t, err)
	return tenant.Context{OrgID: f.tc.OrgID, ProjectID: id, UserID: f.tc.UserID}
}

// SECURITY: same org, other project: the device id is the only thing the caller borrows.
func TestPostgres_Contact_SiblingProjectCannotTouchDeviceContacts(t *testing.T) {
	f := newPgFixture(t)
	f.upsert(t, "628111@s.whatsapp.net", func(c *contact.Contact) { c.Name = "Victim" })
	sibling := f.siblingProject(t)
	ctx := tenant.Into(t.Context(), sibling)

	_, err := f.store.ByJID(ctx, f.device, "628111@s.whatsapp.net")
	require.True(t, contact.IsNotFoundError(err), "a sibling project never reads the row, got %v", err)

	merge := contact.New(sibling.OrgID, sibling.ProjectID, f.device, "628111@s.whatsapp.net")
	merge.Name = "Hijacked"
	err = f.store.Upsert(ctx, merge)
	require.True(t, contact.IsNotFoundError(err), "the conflict clause must refuse another project's row, got %v", err)

	fresh := contact.New(sibling.OrgID, sibling.ProjectID, f.device, "628999@s.whatsapp.net")
	err = f.store.Upsert(ctx, fresh)
	require.True(t, contact.IsNotFoundError(err), "an insert on another project's device must be refused, got %v", err)

	got, err := f.store.ByJID(f.ctx(t), f.device, "628111@s.whatsapp.net")
	require.NoError(t, err)
	require.Equal(t, "Victim", got.Name)
	_, err = f.store.ByJID(f.ctx(t), f.device, "628999@s.whatsapp.net")
	require.True(t, contact.IsNotFoundError(err), "no row was planted on the device")
}
