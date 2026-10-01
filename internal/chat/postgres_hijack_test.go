package chat_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

// SECURITY: the fixture role bypasses RLS, so these prove the store's own org predicate.
func TestPostgres_Chat_OtherOrgIsInvisibleWithoutRLS(t *testing.T) {
	f := newPgFixture(t)
	victim := f.ctx(t)
	c := f.newChat(t, "628111@s.whatsapp.net", "77@lid")
	c.Rename("Victim")
	require.NoError(t, f.store.Save(victim, c, 0))

	attacker := tenant.Into(t.Context(), f.seed.Tenant(t))
	_, err := f.store.ByID(attacker, c.ID)
	require.True(t, chat.IsNotFoundError(err), "got %v", err)
	_, err = f.store.ByJID(attacker, f.device, "77@lid")
	require.True(t, chat.IsNotFoundError(err), "got %v", err)
	page, _, err := f.store.List(attacker, chat.ListOpts{}.WithDefaults())
	require.NoError(t, err)
	require.Empty(t, page)
	require.True(t, chat.IsNotFoundError(f.store.Delete(attacker, c.ID)))

	hijack := *c
	hijack.Name = "Hijacked"
	err = f.store.Save(attacker, &hijack, 0)
	require.True(t, chat.IsNotFoundError(err), "an upsert carrying another org's row id must be refused, got %v", err)
	got, err := f.store.ByID(victim, c.ID)
	require.NoError(t, err)
	require.Equal(t, "Victim", got.Name)
}

func (f pgFixture) count(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.seed.SQL.QueryRowContext(t.Context(),
		"SELECT count(*) FROM "+f.seed.Prefix+"chats WHERE "+where, args...).Scan(&n))
	return n
}

// SECURITY: a row naming another org, another project or another project's device must never be written.
func TestPostgres_Chat_WriteRefusesForeignScopeWithoutRLS(t *testing.T) {
	f := newPgFixture(t)
	attacker := f.seed.Tenant(t)
	attackerCtx := tenant.Into(t.Context(), attacker)
	sibling := f.seed.Project(t, f.tc)
	siblingDevice := f.seed.Device(t, sibling)

	crossOrg := f.newChat(t, "628111@s.whatsapp.net", "")
	_, err := f.store.Insert(attackerCtx, crossOrg)
	require.True(t, chat.IsNotFoundError(err), "cross-org insert, got %v", err)
	require.True(t, chat.IsNotFoundError(f.store.Save(attackerCtx, crossOrg, 0)), "cross-org save")

	otherProject, err := chat.New(f.tc.OrgID, sibling.ProjectID, siblingDevice, fakes.ChatPublicID(), "628222@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	_, err = f.store.Insert(f.ctx(t), otherProject)
	require.True(t, chat.IsNotFoundError(err), "same-org other-project insert, got %v", err)
	require.True(t, chat.IsNotFoundError(f.store.Save(f.ctx(t), otherProject, 0)), "same-org other-project save")

	foreignDevice, err := chat.New(f.tc.OrgID, f.tc.ProjectID, siblingDevice, fakes.ChatPublicID(), "628333@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	_, err = f.store.Insert(f.ctx(t), foreignDevice)
	require.True(t, chat.IsNotFoundError(err), "a device of another project, got %v", err)
	require.True(t, chat.IsNotFoundError(f.store.Save(f.ctx(t), foreignDevice, 0)))

	require.Zero(t, f.count(t, "jid IN ('628111@s.whatsapp.net','628222@s.whatsapp.net','628333@s.whatsapp.net')"), "nothing written")
}

// SECURITY: bare-id and public-id reads are scoped to the caller's org and project.
func TestPostgres_Chat_PublicReadsAreScoped(t *testing.T) {
	f := newPgFixture(t)
	c := f.newChat(t, "628111@s.whatsapp.net", "")
	require.NoError(t, f.store.Save(f.ctx(t), c, 0))
	sibling := f.seed.Project(t, f.tc)
	siblingDevice := f.seed.Device(t, sibling)
	siblingChat := f.seed.Chat(t, sibling, siblingDevice, "628999@s.whatsapp.net")

	attacker := tenant.Into(t.Context(), f.seed.Tenant(t))
	_, err := f.store.ByPublicID(attacker, c.PublicID)
	require.True(t, chat.IsNotFoundError(err), "got %v", err)
	ids, err := f.store.PublicIDs(attacker, []uuid.UUID{c.ID})
	require.NoError(t, err)
	require.Empty(t, ids)

	ids, err = f.store.PublicIDs(f.ctx(t), []uuid.UUID{c.ID, siblingChat})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{c.ID: c.PublicID}, ids, "a sibling project's chat id is not mapped")

	page, _, err := f.store.List(f.ctx(t), chat.ListOpts{}.WithDefaults())
	require.NoError(t, err)
	require.Len(t, page, 1, "List never crosses into the sibling project")
	require.Equal(t, c.ID, page[0].ID)
}
