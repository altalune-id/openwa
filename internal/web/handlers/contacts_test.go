package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
)

type contactsFixture struct {
	*handlerFixture
	mux       *http.ServeMux
	contacts  *contact.Service
	store     *fakes.Contact
	uid       uuid.UUID
	orgID     uuid.UUID
	device    *device.Device
	secondDev *device.Device
	devices   *device.Service
	projectID uuid.UUID
	ctx       context.Context
}

func newContactsFixture(t *testing.T) *contactsFixture {
	t.Helper()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	proj, err := f.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	ctx := tenant.Into(context.Background(), tenant.Context{OrgID: o.ID, ProjectID: proj.ID, UserID: uid})

	devStore := fakes.NewDevice()
	d, err := device.New(o.ID, proj.ID, fakes.DevicePublicID(), "sales-01")
	require.NoError(t, err)
	require.NoError(t, devStore.Save(ctx, d, 0))
	d2, err := device.New(o.ID, proj.ID, fakes.DevicePublicID(), "support-02")
	require.NoError(t, err)
	require.NoError(t, devStore.Save(ctx, d2, 0))
	devices := device.NewService(devStore, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, &fakes.DeviceSessions{})

	store := fakes.NewContact()
	contacts := contact.NewService(store, discardLogger(), passthroughUnexpected())
	mux := http.NewServeMux()
	handlers.NewContactsHandler(f.Deps, f.Projects, contacts, devices).Register(mux)
	return &contactsFixture{handlerFixture: f, mux: mux, contacts: contacts, store: store, uid: uid, orgID: o.ID, device: d, secondDev: d2, devices: devices, projectID: proj.ID, ctx: ctx}
}

func (c *contactsFixture) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c.mux.ServeHTTP(rec, c.authedRequest(t, http.MethodGet, target, "", session.Principal{UserID: c.uid, ActiveOrgID: c.orgID}))
	return rec
}

func TestContacts_ListsWithAMessageButton(t *testing.T) {
	c := newContactsFixture(t)
	require.NoError(t, c.contacts.UpsertFromEngine(c.ctx, contact.ContactInput{DeviceID: c.device.ID, JID: "628111222333@s.whatsapp.net", Phone: "628111222333", Name: "Budi"}))
	c.store.Seed(contact.New(c.orgID, uuid.New(), c.device.ID, "999@lid"))

	rec := c.get(t, "/orgs/acme/projects/alpha/contacts?q=bu")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	require.Contains(t, body, "Budi")
	require.Contains(t, body, `data-copy="628111222333"`)
	require.Contains(t, body, "/inbox/new?device="+c.device.PublicID+"&amp;phone=628111222333")
	require.NotContains(t, body, c.device.ID.String(), "SECURITY: no device UUID reaches the page")
	require.NotContains(t, body, "999@lid", "a sibling project's contact never lists")
}

func TestContacts_DeviceFilterNarrowsRows(t *testing.T) {
	c := newContactsFixture(t)
	require.NoError(t, c.contacts.UpsertFromEngine(c.ctx, contact.ContactInput{DeviceID: c.device.ID, JID: "1@s.whatsapp.net", Phone: "1", Name: "Alice"}))
	require.NoError(t, c.contacts.UpsertFromEngine(c.ctx, contact.ContactInput{DeviceID: c.secondDev.ID, JID: "2@s.whatsapp.net", Phone: "2", Name: "Bella"}))

	body := c.get(t, "/orgs/acme/projects/alpha/contacts?device="+c.secondDev.PublicID).Body.String()
	require.Contains(t, body, "Bella")
	require.NotContains(t, body, "Alice")
}

func TestContacts_HiddenNumberHasNoMessageButton(t *testing.T) {
	c := newContactsFixture(t)
	require.NoError(t, c.contacts.UpsertFromEngine(c.ctx, contact.ContactInput{DeviceID: c.device.ID, JID: "999@lid", Name: "Hidden"}))

	body := c.get(t, "/orgs/acme/projects/alpha/contacts").Body.String()
	require.Contains(t, body, "Hidden")
	require.NotContains(t, body, "/inbox/new")
}

func TestContacts_UnknownDeviceHasNoMessageButton(t *testing.T) {
	c := newContactsFixture(t)
	c.store.Seed(contact.New(c.orgID, c.projectID, uuid.New(), "628555@s.whatsapp.net"))
	require.NoError(t, c.contacts.UpsertFromEngine(c.ctx, contact.ContactInput{DeviceID: uuid.New(), JID: "628777@s.whatsapp.net", Phone: "628777", Name: "Orphan"}))

	body := c.get(t, "/orgs/acme/projects/alpha/contacts").Body.String()
	require.Contains(t, body, "Orphan")
	require.NotContains(t, body, "/inbox/new")
}

func TestContacts_MalformedCursorIs400(t *testing.T) {
	c := newContactsFixture(t)
	require.Equal(t, http.StatusBadRequest, c.get(t, "/orgs/acme/projects/alpha/contacts?cursor=%21%21").Code)
}

type failingContactStore struct{ *fakes.Contact }

func (failingContactStore) List(context.Context, contact.ListOpts) ([]*contact.Contact, string, error) {
	return nil, "", errors.New("db down")
}

func TestContacts_StoreFailureIs500(t *testing.T) {
	c := newContactsFixture(t)
	svc := contact.NewService(failingContactStore{c.store}, discardLogger(), passthroughUnexpected())
	mux := http.NewServeMux()
	handlers.NewContactsHandler(c.Deps, c.Projects, svc, c.devices).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, c.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/contacts", "", session.Principal{UserID: c.uid, ActiveOrgID: c.orgID}))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestContacts_LoadMoreEscapesTheCursor(t *testing.T) {
	c := newContactsFixture(t)
	for i := range contact.DefaultListLimit + 1 {
		phone := "62" + strconv.Itoa(1000+i)
		require.NoError(t, c.contacts.UpsertFromEngine(c.ctx, contact.ContactInput{DeviceID: c.device.ID, JID: phone + "@s.whatsapp.net", Phone: phone, Name: "Nina " + phone}))
	}
	body := c.get(t, "/orgs/acme/projects/alpha/contacts?q=Nina").Body.String()
	require.Contains(t, body, "cursor=")
}
