package contact_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/tenantseed"
)

type pgFixture struct {
	seed   tenantseed.DB
	store  contact.Store
	tc     tenant.Context
	device uuid.UUID
}

func newPgFixture(t *testing.T) pgFixture {
	t.Helper()
	seed := tenantseed.Open(t)
	tc := seed.Tenant(t)
	return pgFixture{seed: seed, store: contact.NewStore(seed.Cfg, seed.Pool, seed.PC), tc: tc, device: seed.Device(t, tc)}
}

func (f pgFixture) ctx(t *testing.T) context.Context { return tenant.Into(t.Context(), f.tc) }

func (f pgFixture) upsert(t *testing.T, jid string, mut func(*contact.Contact)) {
	t.Helper()
	c := contact.New(f.tc.OrgID, f.tc.ProjectID, f.device, jid)
	mut(c)
	require.NoError(t, f.store.Upsert(f.ctx(t), c))
}

func TestPostgres_Contact_UpsertMergesNonEmptyFields(t *testing.T) {
	f := newPgFixture(t)
	f.upsert(t, "628111@s.whatsapp.net", func(c *contact.Contact) { c.Name, c.Phone = "Budi Santoso", "628111" })
	f.upsert(t, "628111@s.whatsapp.net", func(c *contact.Contact) { c.PushName, c.LID = "budi", "77@lid" })
	got, err := f.store.ByJID(f.ctx(t), f.device, "628111@s.whatsapp.net")
	require.NoError(t, err)
	require.Equal(t, "Budi Santoso", got.Name)
	require.Equal(t, "budi", got.PushName)
	require.Equal(t, "77@lid", got.LID)
	require.Equal(t, "628111", got.Phone)
}

func TestPostgres_Contact_ListPagesAndSearches(t *testing.T) {
	f := newPgFixture(t)
	for i, name := range []string{"Budi", "Bunga", "Citra", "Dewi"} {
		f.upsert(t, "62811"+string(rune('0'+i))+"@s.whatsapp.net", func(c *contact.Contact) {
			c.Name, c.Phone = name, "62811"+string(rune('0'+i))
			c.UpdatedAt = time.Now().UTC().Truncate(time.Second)
		})
	}
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for {
		page, next, err := f.store.List(f.ctx(t), contact.ListOpts{Limit: 3, Cursor: cursor})
		require.NoError(t, err)
		for _, c := range page {
			require.False(t, seen[c.ID], "no row on two pages")
			seen[c.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	require.Len(t, seen, 4)

	page, _, err := f.store.List(f.ctx(t), contact.ListOpts{Search: "bu"})
	require.NoError(t, err)
	require.Len(t, page, 2)
	page, _, err = f.store.List(f.ctx(t), contact.ListOpts{Search: "628112"})
	require.NoError(t, err)
	require.Empty(t, page, "the search is on the name only, the column the index covers")
}

func TestPostgres_Contact_ListFiltersByDevice(t *testing.T) {
	f := newPgFixture(t)
	other := f.seed.Device(t, f.tc)
	third := f.seed.Device(t, f.tc)
	for _, d := range []uuid.UUID{f.device, other, third} {
		c := contact.New(f.tc.OrgID, f.tc.ProjectID, d, "628111@s.whatsapp.net")
		require.NoError(t, f.store.Upsert(f.ctx(t), c))
	}
	count := func(o contact.ListOpts) int {
		page, _, err := f.store.List(f.ctx(t), o)
		require.NoError(t, err)
		return len(page)
	}
	require.Equal(t, 3, count(contact.ListOpts{}))
	require.Equal(t, 1, count(contact.ListOpts{DeviceID: &other}))
	require.Equal(t, 2, count(contact.ListOpts{DeviceIDs: []uuid.UUID{f.device, third}}))
	require.Equal(t, 1, count(contact.ListOpts{DeviceID: &other, DeviceIDs: []uuid.UUID{other, third}}))
	require.Zero(t, count(contact.ListOpts{DeviceIDs: []uuid.UUID{uuid.New()}}))
}

func TestPostgres_Contact_ListIsolatesProjectAndOrg(t *testing.T) {
	f := newPgFixture(t)
	f.upsert(t, "628111@s.whatsapp.net", func(c *contact.Contact) { c.Name = "Mine" })

	sibling := f.siblingProject(t)
	siblingDevice := f.seed.Device(t, sibling)
	require.NoError(t, f.store.Upsert(tenant.Into(t.Context(), sibling), contact.New(sibling.OrgID, sibling.ProjectID, siblingDevice, "628222@s.whatsapp.net")))

	page, _, err := f.store.List(f.ctx(t), contact.ListOpts{})
	require.NoError(t, err)
	require.Len(t, page, 1, "a sibling project's contacts stay out")
	require.Equal(t, "628111@s.whatsapp.net", page[0].JID)

	otherTC := f.seed.Tenant(t)
	otherDevice := f.seed.Device(t, otherTC)
	require.NoError(t, f.store.Upsert(tenant.Into(t.Context(), otherTC), contact.New(otherTC.OrgID, otherTC.ProjectID, otherDevice, "628333@s.whatsapp.net")))

	page, _, err = f.store.List(f.ctx(t), contact.ListOpts{DeviceIDs: []uuid.UUID{f.device, otherDevice, siblingDevice}})
	require.NoError(t, err)
	require.Len(t, page, 1, "another org's and project's contacts stay out even when their device ids are named")
}
