package chat_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/platform/tenant"
)

func TestListGroups_MergesLiveWithStored(t *testing.T) {
	f := newSvc(t)
	f.groups.Joined = []chat.GroupInfo{{JID: "111@g.us", Name: "Tim Sales", Participants: 12}}
	left, err := f.svc.EnsureForJID(f.ctx(t), f.device, "222@g.us", "", chat.KindGroup, "Old group")
	require.NoError(t, err)

	got, err := f.svc.ListGroups(f.ctx(t), f.device)
	require.NoError(t, err)
	require.Len(t, got, 2)
	byJID := map[string]chat.GroupView{}
	for _, g := range got {
		byJID[g.JID] = g
	}
	require.True(t, byJID["111@g.us"].Joined)
	require.Equal(t, 12, byJID["111@g.us"].Participants)
	require.Equal(t, uuid.Nil, byJID["111@g.us"].ChatID, "a live group with no stored chat has none yet")
	require.Equal(t, 1, f.store.Len(), "a GET writes no rows; chats appear on join or on the first message")
	require.False(t, byJID["222@g.us"].Joined, "a stored group the engine no longer lists is shown as left")
	require.Equal(t, left.ID, byJID["222@g.us"].ChatID)
}

func TestListGroups_LiveGroupCarriesItsStoredChat(t *testing.T) {
	f := newSvc(t)
	stored, err := f.svc.EnsureForJID(f.ctx(t), f.device, "111@g.us", "", chat.KindGroup, "Tim")
	require.NoError(t, err)
	f.groups.Joined = []chat.GroupInfo{{JID: "111@g.us", Name: "Tim"}}
	got, err := f.svc.ListGroups(f.ctx(t), f.device)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].Joined)
	require.Equal(t, stored.ID, got[0].ChatID)
}

func TestGroupVerbs_EngineFailuresAreReported(t *testing.T) {
	f := newSvc(t)
	g, err := f.svc.EnsureForJID(f.ctx(t), f.device, "666@g.us", "", chat.KindGroup, "")
	require.NoError(t, err)

	f.groups.Err = errors.New("engine down")
	_, err = f.svc.ListGroups(f.ctx(t), f.device)
	require.Error(t, err)
	_, err = f.svc.GroupInfo(f.ctx(t), g.ID)
	require.Error(t, err)
	_, err = f.svc.JoinGroup(f.ctx(t), f.device, "https://chat.whatsapp.com/AbC")
	require.Error(t, err)
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, ae.GRPCCode())
}

func TestGroupVerbs_AnEngineErrorWithACodePassesThrough(t *testing.T) {
	f := newSvc(t)
	g, err := f.svc.EnsureForJID(f.ctx(t), f.device, "777@g.us", "", chat.KindGroup, "")
	require.NoError(t, err)
	coded := apperror.New("WAS999", "session not connected", codes.FailedPrecondition)
	f.groups.Err = coded
	_, err = f.svc.GroupInfo(f.ctx(t), g.ID)
	require.Same(t, coded, err)
}

func TestGroupInfo_ReturnsTheEngineDetails(t *testing.T) {
	f := newSvc(t)
	g, err := f.svc.EnsureForJID(f.ctx(t), f.device, "888@g.us", "", chat.KindGroup, "")
	require.NoError(t, err)
	f.groups.Infos = map[string]chat.GroupInfo{"888@g.us": {JID: "888@g.us", Name: "Ops", Participants: 4}}
	got, err := f.svc.GroupInfo(f.ctx(t), g.ID)
	require.NoError(t, err)
	require.Equal(t, 4, got.Participants)
}

func TestGroupInfo_RefusesADirectChat(t *testing.T) {
	f := newSvc(t)
	dm, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	_, err = f.svc.GroupInfo(f.ctx(t), dm.ID)
	require.True(t, chat.IsNotAGroupError(err))
	_, err = f.svc.LeaveGroup(f.ctx(t), dm.ID)
	require.True(t, chat.IsNotAGroupError(err))
	require.Empty(t, f.groups.Left, "SECURITY: a direct chat never reaches the engine")
}

func TestGroupVerbs_AForeignProjectChatIsNotFound(t *testing.T) {
	f := newSvc(t)
	g, err := f.svc.EnsureForJID(f.ctx(t), f.device, "999@g.us", "", chat.KindGroup, "")
	require.NoError(t, err)
	other := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, ProjectID: uuid.New(), UserID: f.tc.UserID})
	_, err = f.svc.GroupInfo(other, g.ID)
	require.True(t, chat.IsNotFoundError(err))
	_, err = f.svc.LeaveGroup(other, g.ID)
	require.True(t, chat.IsNotFoundError(err))
	require.Empty(t, f.groups.Left)
}

func TestJoinGroup_CreatesTheChatAfterTheEngineAnswers(t *testing.T) {
	f := newSvc(t)
	f.groups.JoinJID = "333@g.us"
	f.groups.Infos = map[string]chat.GroupInfo{"333@g.us": {JID: "333@g.us", Name: "Komunitas"}}
	c, err := f.svc.JoinGroup(f.ctx(t), f.device, "https://chat.whatsapp.com/AbC")
	require.NoError(t, err)
	require.Equal(t, "333@g.us", c.JID)
	require.Equal(t, "Komunitas", c.Name)
	require.Equal(t, chat.KindGroup, c.Kind)
}

func TestLeaveGroup_ArchivesAfterTheEngineLeaves(t *testing.T) {
	f := newSvc(t)
	g, err := f.svc.EnsureForJID(f.ctx(t), f.device, "444@g.us", "", chat.KindGroup, "Bye")
	require.NoError(t, err)
	got, err := f.svc.LeaveGroup(f.ctx(t), g.ID)
	require.NoError(t, err)
	require.True(t, got.Archived)
	require.Equal(t, []string{"444@g.us"}, f.groups.Left)

	f.groups.Err = errors.New("engine down")
	g2, err := f.svc.EnsureForJID(f.ctx(t), f.device, "555@g.us", "", chat.KindGroup, "")
	require.NoError(t, err)
	_, err = f.svc.LeaveGroup(f.ctx(t), g2.ID)
	require.Error(t, err)
	still, err := f.svc.Get(f.ctx(t), g2.ID)
	require.NoError(t, err)
	require.False(t, still.Archived, "an engine failure leaves the chat as it was")
}
