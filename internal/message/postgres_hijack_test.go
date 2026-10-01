package message_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/tenant"
)

// SECURITY: the fixture role bypasses RLS, so these prove the store's own org predicate.
func TestPostgres_Message_OtherOrgIsInvisibleWithoutRLS(t *testing.T) {
	f := newPgFixture(t)
	victim := f.queued(t, "secret")
	attackerTC := f.seed.Tenant(t)
	attacker := tenant.Into(t.Context(), attackerTC)

	_, err := f.store.ByID(attacker, victim.ID)
	require.True(t, message.IsNotFoundError(err))
	_, err = f.store.ByWAID(attacker, f.device, victim.WAMessageID)
	require.True(t, message.IsNotFoundError(err))
	claimed, err := f.store.ClaimNext(attacker, f.device, time.Hour)
	require.NoError(t, err)
	require.Nil(t, claimed, "another org cannot drain this device's queue")
	n, _, err := f.store.DeleteOlderThan(attacker, f.tc.ProjectID, time.Now().Add(time.Hour), 100)
	require.NoError(t, err)
	require.Zero(t, n)

	hijack := *victim
	hijack.Status = message.StatusFailed
	err = f.store.Save(attacker, &hijack, 0)
	require.True(t, message.IsNotFoundError(err), "got %v", err)

	p, err := message.NewRetentionPolicy(attackerTC.OrgID, f.tc.ProjectID, 1)
	require.NoError(t, err)
	require.NoError(t, f.store.SaveRetention(f.ctx(t), &message.RetentionPolicy{ProjectID: f.tc.ProjectID, OrgID: f.tc.OrgID, Days: 30}))
	require.True(t, message.IsNotFoundError(f.store.SaveRetention(attacker, p)), "another org cannot rewrite a project's retention")
}

func TestPostgres_Message_OtherOrgCannotReadOrMutateWithoutRLS(t *testing.T) {
	f := newPgFixture(t)
	victim := f.inbound(t, "V1", time.Now())
	attacker := tenant.Into(t.Context(), f.seed.Tenant(t))

	_, err := f.store.ByPublicID(attacker, victim.PublicID)
	require.True(t, message.IsNotFoundError(err))
	ids, err := f.store.PublicIDs(attacker, []uuid.UUID{victim.ID})
	require.NoError(t, err)
	require.Empty(t, ids)
	_, err = f.store.QuotedByWAID(attacker, f.device, victim.WAMessageID)
	require.True(t, message.IsNotFoundError(err))
	n, err := f.store.MarkChatRead(attacker, f.chat, uuid.Max, time.Now())
	require.NoError(t, err)
	require.Zero(t, n)
	unread, err := f.store.CountUnreadInbound(attacker, f.chat)
	require.NoError(t, err)
	require.Zero(t, unread)
	rows, err := f.store.UnsentBefore(attacker, f.tc.ProjectID, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Empty(t, rows)
	cnt, err := f.store.CountOlderThan(attacker, f.tc.ProjectID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Zero(t, cnt)

	got, err := f.store.ByID(f.ctx(t), victim.ID)
	require.NoError(t, err)
	require.Nil(t, got.ReadAt, "the victim's row is untouched")
}
