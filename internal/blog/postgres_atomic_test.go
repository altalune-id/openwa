package blog_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/blog"
	"altalune.id/openwa/internal/platform/tenant"
)

type taggedFixture struct {
	svc  *blog.Service
	ctx  context.Context
	cat  uuid.UUID
	tagA uuid.UUID
	tagB uuid.UUID
}

func newTaggedFixture(t *testing.T) taggedFixture {
	t.Helper()
	f := newPgFixture(t)
	svc, _, _ := newHooked(t, f.store, f.uow)
	return taggedFixture{
		svc:  svc,
		ctx:  tenant.Into(t.Context(), f.tc),
		cat:  f.cat,
		tagA: seedPgTag(t, f.sqlDB, f.prefix, f.tc),
		tagB: seedPgTag(t, f.sqlDB, f.prefix, f.tc),
	}
}

func TestPostgres_UpdateWithTagsSemantics(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(t *testing.T, f taggedFixture)
	}{
		{
			name: "one update is one write, so no unconditional tail can follow it",
			run: func(t *testing.T, f taggedFixture) {
				t.Helper()
				p, err := f.svc.Create(f.ctx, f.cat, "Original", "original", "body")
				require.NoError(t, err)

				got, err := f.svc.UpdateWithTags(f.ctx, p.ID, "Edited", "original", "b", f.cat, []uuid.UUID{f.tagA, f.tagB}, p.Version)
				require.NoError(t, err)
				assert.Equal(t, p.Version+1, got.Version)

				stored, err := f.svc.ByID(f.ctx, p.ID)
				require.NoError(t, err)
				assert.Equal(t, p.Version+1, stored.Version)
				assert.ElementsMatch(t, []uuid.UUID{f.tagA, f.tagB}, stored.TagIDs)
			},
		},
		{
			name: "an empty tag set clears the tags under the same precondition",
			run: func(t *testing.T, f taggedFixture) {
				t.Helper()
				p, err := f.svc.Create(f.ctx, f.cat, "Original", "original", "body")
				require.NoError(t, err)
				tagged, err := f.svc.UpdateWithTags(f.ctx, p.ID, "Tagged", "original", "b", f.cat, []uuid.UUID{f.tagA}, p.Version)
				require.NoError(t, err)

				cleared, err := f.svc.UpdateWithTags(f.ctx, p.ID, "Cleared", "original", "c", f.cat, nil, tagged.Version)
				require.NoError(t, err)
				assert.Empty(t, cleared.TagIDs)

				stored, err := f.svc.ByID(f.ctx, p.ID)
				require.NoError(t, err)
				assert.Empty(t, stored.TagIDs)
			},
		},
		{
			name: "ifVersion 0 stays last-write-wins for the console",
			run: func(t *testing.T, f taggedFixture) {
				t.Helper()
				p, err := f.svc.Create(f.ctx, f.cat, "Original", "original", "body")
				require.NoError(t, err)
				_, err = f.svc.UpdateWithTags(f.ctx, p.ID, "A body", "original", "a", f.cat, []uuid.UUID{f.tagA}, p.Version)
				require.NoError(t, err)

				got, err := f.svc.UpdateWithTags(f.ctx, p.ID, "B body", "original", "b", f.cat, []uuid.UUID{f.tagB}, 0)
				require.NoError(t, err, "an unconditional update keeps today's console semantics")
				assert.Equal(t, []uuid.UUID{f.tagB}, got.TagIDs)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, newTaggedFixture(t))
		})
	}
}

func TestPostgres_EnqueueFailureRollsBackTheWrite(t *testing.T) {
	f := newPgFixture(t)
	svc, unex, hooks := newHooked(t, f.store, f.uow)
	ctx := tenant.Into(t.Context(), f.tc)

	draft, err := svc.Create(ctx, f.cat, "Draft", "", "body")
	require.NoError(t, err)
	live, err := svc.Create(ctx, f.cat, "Live", "", "body")
	require.NoError(t, err)
	live, err = svc.Publish(ctx, live.ID, 0)
	require.NoError(t, err)
	require.Len(t, hooks.Recorded(), 1)
	require.True(t, hooks.Recorded()[0].InTx)

	hooks.Err = errors.New("outbox down")

	_, err = svc.Publish(ctx, draft.ID, 0)
	require.Error(t, err)
	got, err := svc.ByID(ctx, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, blog.StatusDraft, got.Status, "a failed enqueue must roll the publish back")
	assert.Equal(t, draft.Version, got.Version)

	require.Error(t, svc.Delete(ctx, live.ID, 0))
	got, err = svc.ByID(ctx, live.ID)
	require.NoError(t, err, "a failed enqueue must roll the delete back")
	assert.Equal(t, blog.StatusPublished, got.Status)

	assert.Equal(t, 2, *unex)
}
