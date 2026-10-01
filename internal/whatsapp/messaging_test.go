package whatsapp_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

type deadlinePort struct {
	messages []whatsapp.InboundMessage
	scoped   bool
}

func (r *deadlinePort) RecordInbound(ctx context.Context, _ whatsapp.SessionRef, m whatsapp.InboundMessage) error {
	_, r.scoped = ctx.Deadline()
	r.messages = append(r.messages, m)
	return nil
}

func (r *deadlinePort) RecordReceipt(context.Context, whatsapp.SessionRef, whatsapp.Receipt) error {
	return nil
}

func (r *deadlinePort) UpsertContact(context.Context, whatsapp.SessionRef, whatsapp.ContactUpdate) error {
	return nil
}

func TestService_ForwardsInboundWithItsOwnDeadline(t *testing.T) {
	svc := newSinkFixture(t).svc
	in := &deadlinePort{}
	require.NoError(t, svc.SetInbound(in))
	require.True(t, whatsapp.IsSetupError(svc.SetInbound(in)))
	svc.OnMessage(whatsapp.SessionRef{DeviceID: uuid.New()}, whatsapp.InboundMessage{ID: "A"})
	require.Len(t, in.messages, 1)
	require.True(t, in.scoped, "each forward carries its own 10 s budget")
}

func TestService_MessagingNeedsALiveSession(t *testing.T) {
	svc := newSinkFixture(t).svc
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	_, err := svc.GroupList(ctx, uuid.New())
	require.True(t, whatsapp.IsNotOwnedError(err), "a device this process does not hold is not owned, got %v", err)
}

func TestService_MessagingNeedsATenant(t *testing.T) {
	svc := newSinkFixture(t).svc
	_, err := svc.GroupList(t.Context(), uuid.New())
	require.Error(t, err)
	require.False(t, whatsapp.IsNotOwnedError(err))
}

func TestService_MessagingRefusesASessionWithoutMessaging(t *testing.T) {
	f := newSinkFixture(t)
	plain := &fakes.EngineSession{}
	f.rt.Hold(f.ref, plain)
	owner := tenant.Into(t.Context(), tenant.Context{OrgID: f.ref.OrgID, ProjectID: f.ref.ProjectID})
	_, err := f.svc.GroupList(owner, f.ref.DeviceID)
	require.True(t, whatsapp.IsUnsupportedError(err))
}

func TestService_OwnerReachesEveryTransportCall(t *testing.T) {
	f := newSinkFixture(t)
	sess := fakes.NewMessagingSession()
	sess.Groups = []whatsapp.GroupInfo{{JID: "1@g.us", Name: "G"}}
	sess.JoinJID = "2@g.us"
	sess.OnWA = map[string]string{"628111": "628111@s.whatsapp.net"}
	sess.Media = []byte("body")
	f.rt.Hold(f.ref, sess)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.ref.OrgID, ProjectID: f.ref.ProjectID})
	id := f.ref.DeviceID

	info, err := f.svc.GroupInfo(ctx, id, "1@g.us")
	require.NoError(t, err)
	require.Equal(t, "G", info.Name)
	jid, err := f.svc.GroupJoin(ctx, id, "https://chat.whatsapp.com/X")
	require.NoError(t, err)
	require.Equal(t, "2@g.us", jid)
	require.NoError(t, f.svc.GroupLeave(ctx, id, "1@g.us"))
	require.NoError(t, f.svc.MarkRead(ctx, id, "1@g.us", "", []string{"A"}, false))
	require.Equal(t, [][]string{{"A"}}, sess.Reads)
	found, err := f.svc.IsOnWhatsApp(ctx, id, []string{"628111"})
	require.NoError(t, err)
	require.Equal(t, sess.OnWA, found)
	file, err := f.svc.FetchMedia(ctx, id, whatsapp.MediaKeys{DirectPath: "/v"}, "image")
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

// SECURITY: every transport and group path goes through messaging(ctx, id), so each one refuses a device held for another tenant.
func TestService_MessagingRefusesAnotherTenantsDevice(t *testing.T) {
	f := newSinkFixture(t)
	sess := fakes.NewMessagingSession()
	sess.Groups = []whatsapp.GroupInfo{{JID: "1@g.us", Name: "Victim group", InviteLink: "https://chat.whatsapp.com/SECRET"}}
	sess.JoinJID = "2@g.us"
	f.rt.Hold(f.ref, sess)
	owner := tenant.Into(t.Context(), tenant.Context{OrgID: f.ref.OrgID, ProjectID: f.ref.ProjectID})
	sibling := tenant.Into(t.Context(), tenant.Context{OrgID: f.ref.OrgID, ProjectID: uuid.New()})
	stranger := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	id := f.ref.DeviceID

	groups, err := f.svc.GroupList(owner, id)
	require.NoError(t, err)
	require.Len(t, groups, 1)

	for name, ctx := range map[string]context.Context{"another org": stranger, "a sibling project": sibling} {
		calls := map[string]error{}
		_, calls["GroupList"] = f.svc.GroupList(ctx, id)
		_, calls["GroupInfo"] = f.svc.GroupInfo(ctx, id, "1@g.us")
		_, calls["GroupJoin"] = f.svc.GroupJoin(ctx, id, "https://chat.whatsapp.com/X")
		calls["GroupLeave"] = f.svc.GroupLeave(ctx, id, "1@g.us")
		calls["MarkRead"] = f.svc.MarkRead(ctx, id, "1@g.us", "3@lid", []string{"A"}, false)
		_, calls["FetchMedia"] = f.svc.FetchMedia(ctx, id, whatsapp.MediaKeys{DirectPath: "/v"}, "image")
		_, calls["IsOnWhatsApp"] = f.svc.IsOnWhatsApp(ctx, id, []string{"628111"})
		for call, err := range calls {
			require.True(t, whatsapp.IsNotOwnedError(err), "%s from %s: got %v", call, name, err)
		}
	}
	require.Empty(t, sess.Reads, "nothing reached the victim's session")
	require.NotContains(t, sess.Recorded(), "leave")
}
