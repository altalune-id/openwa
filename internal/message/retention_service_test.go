package message_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

func TestRetention_DefaultSetAndPending(t *testing.T) {
	e := newEnv(t)
	days, err := e.svc.Retention(e.ctx(t))
	require.NoError(t, err)
	require.Equal(t, 30, days)
	require.True(t, message.IsInvalidRetentionError(e.svc.SetRetention(e.ctx(t), 0)))
	require.NoError(t, e.svc.SetRetention(e.ctx(t), 1))
	days, err = e.svc.Retention(e.ctx(t))
	require.NoError(t, err)
	require.Equal(t, 1, days)

	ref := message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
	e.store.Seed(message.NewInbound(ref, uuid.New(), fakes.MessagePublicID(), message.InboundInput{WAID: "OLD", Type: "text", Timestamp: e.now.AddDate(0, 0, -2)}))
	e.store.Seed(message.NewInbound(ref, uuid.New(), fakes.MessagePublicID(), message.InboundInput{WAID: "NEW", Type: "text", Timestamp: e.now}))
	n, err := e.svc.PendingDeletion(e.ctx(t))
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func TestRunRetention_DeletesPerProjectAndRepairsChats(t *testing.T) {
	e := newEnv(t)
	other := uuid.New()
	e.tenants.Projects = []uuid.UUID{e.tc.ProjectID, other}
	chat := uuid.New()
	ref := message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
	old := message.NewInbound(ref, chat, fakes.MessagePublicID(), message.InboundInput{WAID: "OLD", Type: "text", Body: "old", Timestamp: e.now.AddDate(0, 0, -40)})
	keep := message.NewInbound(ref, chat, fakes.MessagePublicID(), message.InboundInput{WAID: "KEEP", Type: "text", Body: "keep", Timestamp: e.now.AddDate(0, 0, -1)})
	e.store.Seed(old)
	e.store.Seed(keep)
	otherRef := message.Ref{DeviceID: uuid.New(), OrgID: e.tc.OrgID, ProjectID: other}
	e.store.Seed(message.NewInbound(otherRef, uuid.New(), fakes.MessagePublicID(), message.InboundInput{WAID: "OTHER-OLD", Type: "text", Timestamp: e.now.AddDate(0, 0, -40)}))

	orgOnly := tenant.Into(t.Context(), tenant.Context{OrgID: e.tc.OrgID})
	require.NoError(t, e.svc.RunRetention(orgOnly))

	left := map[string]bool{}
	for _, m := range e.store.All() {
		left[m.WAMessageID] = true
	}
	require.Equal(t, map[string]bool{"KEEP": true}, left, "both projects are swept")
	require.Len(t, e.chats.Repairs, 2)
	var repaired bool
	for _, r := range e.chats.Repairs {
		if r.ID == chat {
			repaired = true
			require.Equal(t, "keep", r.Preview)
			require.Equal(t, 1, r.Unread)
			require.True(t, keep.WATimestamp.Equal(*r.Last))
		}
	}
	require.True(t, repaired)
}

func TestRunRetention_ExpiresRowsQueuedPastADay(t *testing.T) {
	e := newEnv(t)
	e.tenants.Projects = []uuid.UUID{e.tc.ProjectID}
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "never sent"})
	require.NoError(t, err)
	for _, row := range e.store.All() {
		row.CreatedAt = e.now.Add(-message.QueuedExpiry - time.Minute)
		e.store.Seed(row)
	}
	require.NoError(t, e.svc.RunRetention(tenant.Into(t.Context(), tenant.Context{OrgID: e.tc.OrgID})))
	got, err := e.store.ByID(e.ctx(t), m.ID)
	require.NoError(t, err)
	require.Equal(t, message.StatusFailed, got.Status)
	require.Equal(t, message.ReasonExpired, got.Error)
	calls := e.hooks.Recorded()
	require.Equal(t, "failed", calls[len(calls)-1].Data.(events.MessageStatusV1).Status)
}
