package fakes_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

func TestChatSaveMatchesTheRealStoreVersionContract(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	ctx := tenant.Into(t.Context(), tc)
	store := fakes.NewChat()
	c, err := chat.New(tc.OrgID, tc.ProjectID, uuid.New(), fakes.ChatPublicID(), "628111@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, c, 0))
	require.NoError(t, store.Save(ctx, c, 1))
	require.True(t, chat.IsVersionMismatchError(store.Save(ctx, c, 1)))
	got, err := store.ByID(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.Version)

	other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: tc.ProjectID})
	require.True(t, chat.IsNotFoundError(store.Save(other, c, 0)), "the fake keeps the org guard of the real upsert")
}

func TestChatWritesRefuseForeignScope(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	ctx := tenant.Into(t.Context(), tc)
	store := fakes.NewChat()
	for _, scope := range []tenant.Context{
		{OrgID: uuid.New(), ProjectID: tc.ProjectID},
		{OrgID: tc.OrgID, ProjectID: uuid.New()},
	} {
		c, err := chat.New(scope.OrgID, scope.ProjectID, uuid.New(), fakes.ChatPublicID(), "628111@s.whatsapp.net", "", chat.KindDM)
		require.NoError(t, err)
		_, err = store.Insert(ctx, c)
		require.True(t, chat.IsNotFoundError(err))
		require.True(t, chat.IsNotFoundError(store.Save(ctx, c, 0)))
	}
	require.Zero(t, store.Len())
}
