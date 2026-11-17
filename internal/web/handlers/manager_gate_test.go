package handlers_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/web/handlers"
)

const managersOnlyKey = "common.managers_only"

type membershipLookupError struct{}

func (membershipLookupError) Error() string { return "membership lookup failed" }

func addMember(t *testing.T, f *handlerFixture, orgID, owner uuid.UUID, role org.Role) session.Principal {
	t.Helper()
	id := uuid.New()
	_, err := f.Orgs.AddMember(setTenant(context.Background(), orgID, owner), orgID, id, role)
	require.NoError(t, err)
	return session.Principal{UserID: id, ActiveOrgID: orgID}
}

type webhookWriteRoute struct {
	name  string
	setup func(t *testing.T, x *webhookFixture, id string) string
	form  url.Values
	want  int
}

func webhookWriteRoutes() []webhookWriteRoute {
	at := func(suffix string) func(*testing.T, *webhookFixture, string) string {
		return func(_ *testing.T, _ *webhookFixture, id string) string { return webhookBase + "/" + id + suffix }
	}
	return []webhookWriteRoute{
		{name: "new form", setup: func(*testing.T, *webhookFixture, string) string { return webhookBase + "/new" }, want: http.StatusOK},
		{name: "create", setup: func(*testing.T, *webhookFixture, string) string { return webhookBase },
			form: endpointForm("https://example.com/other", string(events.PostPublished)), want: http.StatusOK},
		{name: "update", setup: at(""), form: endpointForm("https://example.com/changed", string(events.PostPublished)), want: http.StatusSeeOther},
		{name: "delete", setup: at("/delete"), want: http.StatusSeeOther},
		{name: "rotate", setup: at("/rotate"), want: http.StatusOK},
		{name: "retire", setup: func(t *testing.T, x *webhookFixture, id string) string {
			t.Helper()
			_, err := x.Hooks.Rotate(x.ctx(), uuid.MustParse(id))
			require.NoError(t, err)
			return webhookBase + "/" + id + "/retire"
		}, want: http.StatusSeeOther},
		{name: "test", setup: at("/test"), want: http.StatusOK},
		{name: "retry", setup: func(t *testing.T, x *webhookFixture, id string) string {
			t.Helper()
			require.NoError(t, x.Hooks.SendTest(x.ctx(), uuid.MustParse(id)))
			x.settle(t, true)
			return webhookBase + "/" + id + "/deliveries/" + x.Outbox.Entries()[0].ID.String() + "/retry"
		}, want: http.StatusOK},
		{name: "retry failed", setup: func(t *testing.T, x *webhookFixture, id string) string {
			t.Helper()
			require.NoError(t, x.Hooks.SendTest(x.ctx(), uuid.MustParse(id)))
			x.settle(t, true)
			return webhookBase + "/" + id + "/retry-failed"
		}, want: http.StatusOK},
	}
}

// SECURITY: a plain member is refused every webhook write and changes nothing.
func TestWebhookHandler_WritesNeedAManager(t *testing.T) {
	t.Parallel()
	for _, rt := range webhookWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			x := newWebhookFixture(t)
			e := x.create(t)
			path := rt.setup(t, x, e.ID.String())
			method := http.MethodPost
			if rt.name == "new form" {
				method = http.MethodGet
			}
			before, err := x.Hooks.ByID(x.ctx(), e.ID)
			require.NoError(t, err)
			queued := len(x.Outbox.Entries())
			member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)

			rec := x.doAs(t, method, path, rt.form, member)
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), managersOnlyKey)

			items, err := x.Hooks.List(x.ctx())
			require.NoError(t, err)
			require.Len(t, items, 1, "the refused request created or deleted an endpoint")
			assert.Equal(t, before.URL, items[0].URL)
			assert.Equal(t, before.Secrets, items[0].Secrets, "the refused request rotated or retired a secret")
			assert.Len(t, x.Outbox.Entries(), queued, "the refused request queued a delivery")
		})
	}
}

func TestWebhookHandler_WritesSucceedForAnAdmin(t *testing.T) {
	t.Parallel()
	for _, rt := range webhookWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			x := newWebhookFixture(t)
			e := x.create(t)
			path := rt.setup(t, x, e.ID.String())
			method := http.MethodPost
			if rt.name == "new form" {
				method = http.MethodGet
			}
			admin := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleAdmin)

			rec := x.doAs(t, method, path, rt.form, admin)
			require.Equal(t, rt.want, rec.Code, rec.Body.String())
			assert.NotContains(t, rec.Body.String(), managersOnlyKey)
		})
	}
}

// SECURITY: a member sees the webhook values but no form that writes.
func TestWebhookHandler_MembersSeeAReadOnlyPage(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	e := x.create(t)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	x.settle(t, true)
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)
	detail := webhookBase + "/" + e.ID.String()

	list := x.doAs(t, http.MethodGet, webhookBase, nil, member)
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), "https://example.com/hook")
	assert.NotContains(t, list.Body.String(), webhookBase+"/new")
	assert.Contains(t, list.Body.String(), managersOnlyKey)

	rec := x.doAs(t, http.MethodGet, detail, nil, member)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "https://example.com/hook")
	assert.Contains(t, body, managersOnlyKey)
	assert.Contains(t, body, detail+"/deliveries/", "delivery details stay readable")
	for _, suffix := range []string{`"`, `/delete"`, `/rotate"`, `/retire"`, `/test"`, `/retry"`, `/retry-failed"`} {
		assert.NotContains(t, body, `action="`+detail+suffix, "a member sees the %s form", suffix)
	}
	assert.NotContains(t, body, `name="url"`)

	mgr := x.do(t, http.MethodGet, detail, nil)
	require.Equal(t, http.StatusOK, mgr.Code)
	assert.Contains(t, mgr.Body.String(), `action="`+detail+`/rotate"`, "a manager keeps the forms")
	assert.NotContains(t, mgr.Body.String(), managersOnlyKey)
}

// SECURITY: a member is refused a rules write and the rules stay as they were.
func TestDevices_RulesNeedAManager(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	path := devicesBase + "/" + d.PublicID
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)

	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, x.authedRequest(t, http.MethodPost, path+"/rules", "version=1&group_mode=open&trigger_prefix=%21bot", member))
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), managersOnlyKey)
	got, err := x.Devices.Get(setTenantProject(context.Background(), x.org, x.project, x.uid), d.ID)
	require.NoError(t, err)
	assert.Empty(t, legacyRules(t, got.Device.Rules).TriggerPrefix, "the refused post changed the rules")
	assert.Equal(t, 1, got.Device.Version)

	admin := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleAdmin)
	rec = httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, x.authedRequest(t, http.MethodPost, path+"/rules", "version=1&group_mode=open&trigger_prefix=%21bot", admin))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
}

// SECURITY: a member sees the rules values but not the rules form.
func TestDevices_MembersSeeReadOnlyRules(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	path := devicesBase + "/" + d.PublicID
	rec := x.do(t, http.MethodPost, path+"/rules", "version=1&group_mode=open&trigger_prefix=%21bot", false)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)

	rec = httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, x.authedRequest(t, http.MethodGet, path, "", member))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `value="!bot"`)
	assert.Contains(t, body, managersOnlyKey)
	assert.NotContains(t, body, `action="`+path+`/rules"`)

	mgr := x.do(t, http.MethodGet, path, "", false)
	assert.Contains(t, mgr.Body.String(), `action="`+path+`/rules"`)
	assert.NotContains(t, mgr.Body.String(), managersOnlyKey)
}

type deviceWriteRoute struct {
	name   string
	method string
	suffix string
	form   string
	htmx   bool
	want   int
}

func deviceWriteRoutes() []deviceWriteRoute {
	return []deviceWriteRoute{
		{name: "new form", method: http.MethodGet, suffix: "/new", want: http.StatusOK},
		{name: "create", method: http.MethodPost, form: "name=Other", want: http.StatusSeeOther},
		{name: "qr link", method: http.MethodPost, suffix: "/{device}/links", htmx: true, want: http.StatusOK},
		{name: "phone link", method: http.MethodPost, suffix: "/{device}/phone-links", form: "phone=%2B62+812+3456", htmx: true, want: http.StatusOK},
		{name: "unlink", method: http.MethodPost, suffix: "/{device}/unlink", want: http.StatusSeeOther},
		{name: "delete", method: http.MethodPost, suffix: "/{device}/delete", want: http.StatusSeeOther},
	}
}

func (x *deviceFixture) doAs(t *testing.T, rt deviceWriteRoute, id string, p session.Principal) *httptest.ResponseRecorder {
	t.Helper()
	req := x.authedRequest(t, rt.method, devicesBase+strings.ReplaceAll(rt.suffix, "{device}", id), rt.form, p)
	if rt.htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, req)
	return rec
}

// SECURITY: a member is refused every number write and nothing changes.
func TestDevices_NumberWritesNeedAManager(t *testing.T) {
	for _, rt := range deviceWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			x := newDeviceFixture(t)
			d := x.seed(t, "Sales")
			member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)

			rec := x.doAs(t, rt, d.PublicID, member)
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), managersOnlyKey)
			assert.Equal(t, 1, x.Store.Len(), "the refused request created or deleted a device")
			assert.Empty(t, x.Sessions.Linked, "the refused request started a QR link")
			assert.Empty(t, x.Sessions.Phones, "the refused request started a phone link")
			assert.Empty(t, x.Sessions.Unlinked, "the refused request logged the number out")
			assert.Empty(t, x.Sessions.Forgot, "the refused request forgot the session")
		})
	}
}

func TestDevices_NumberWritesSucceedForAnAdmin(t *testing.T) {
	for _, rt := range deviceWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			x := newDeviceFixture(t)
			d := x.seed(t, "Sales")
			admin := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleAdmin)

			rec := x.doAs(t, rt, d.PublicID, admin)
			require.Equal(t, rt.want, rec.Code, rec.Body.String())
			assert.NotContains(t, rec.Body.String(), managersOnlyKey)
		})
	}
}

func TestDevices_MembersCanRename(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)

	rec := x.doAs(t, deviceWriteRoute{method: http.MethodPost, suffix: "/{device}/rename", form: "version=1&name=Support"}, d.PublicID, member)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	got, err := x.Devices.Get(setTenantProject(context.Background(), x.org, x.project, x.uid), d.ID)
	require.NoError(t, err)
	assert.Equal(t, "Support", got.Device.Name)
}

// SECURITY: a member sees the numbers and can rename one, but gets no link, unlink or delete action.
func TestDevices_MembersSeeReadOnlyNumberPages(t *testing.T) {
	x := newDeviceFixture(t)
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)
	view := func(path string) string {
		t.Helper()
		rec := x.doAs(t, deviceWriteRoute{method: http.MethodGet, suffix: path}, "", member)
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}
	newHref := `href="` + devicesBase + `/new"`

	empty := view("")
	assert.NotContains(t, empty, newHref, "the empty state offers a member no new number")
	assert.Contains(t, empty, managersOnlyKey)

	d := x.seed(t, "Sales")
	x.Sessions.State = device.LinkState{Outcome: device.LinkNone}
	path := devicesBase + "/" + d.PublicID
	list := view("")
	assert.Contains(t, list, `id="device-`+d.PublicID+`"`)
	assert.NotContains(t, list, newHref)
	assert.Contains(t, list, managersOnlyKey)

	detail := view("/" + d.PublicID)
	for _, suffix := range []string{"/links", "/phone-links", "/unlink", "/delete"} {
		assert.NotContains(t, detail, `action="`+path+suffix+`"`, "a member sees the %s form", suffix)
	}
	assert.Contains(t, detail, `action="`+path+`/rename"`, "a member keeps rename")
	assert.Equal(t, 3, strings.Count(detail, managersOnlyKey), "rules, pairing and danger zone each say who can change them")

	fragment := view("/" + d.PublicID + "/links/current")
	assert.NotContains(t, fragment, `action="`+path+`/links"`, "the polled fragment offers a member no pairing form")
	assert.Contains(t, fragment, managersOnlyKey)

	x.Sessions.State = device.LinkState{ID: "lnk_FakeDeviceLink001", Method: device.LinkPhone, Outcome: device.LinkPending, PNG: []byte{0x89, 'P', 'N', 'G'}, PairingCode: "ABCD-EFGH"}
	png := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'})
	for _, page := range []string{view("/" + d.PublicID), view("/" + d.PublicID + "/links/current")} {
		assert.NotContains(t, page, png, "a member never sees a pending QR")
		assert.NotContains(t, page, "ABCD-EFGH", "a member never sees a pending pairing code")
	}
	mgrPending := x.do(t, http.MethodGet, path+"/links/current", "", false).Body.String()
	assert.Contains(t, mgrPending, "ABCD-EFGH")
	x.Sessions.State = device.LinkState{Outcome: device.LinkNone}

	mgrList := x.do(t, http.MethodGet, devicesBase, "", false).Body.String()
	assert.Contains(t, mgrList, newHref)
	assert.NotContains(t, mgrList, managersOnlyKey)
	mgr := x.do(t, http.MethodGet, path, "", false).Body.String()
	for _, suffix := range []string{"/links", "/phone-links", "/unlink", "/delete", "/rename"} {
		assert.Contains(t, mgr, `action="`+path+suffix+`"`, "a manager keeps the %s form", suffix)
	}
	assert.NotContains(t, mgr, managersOnlyKey)
}

// SECURITY: renaming a project needs an owner or admin.
func TestProjects_RenameNeedsAManager(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner := uuid.New()
	o := f.seedOrg(t, "acme", owner)
	proj, err := f.Projects.Create(setTenant(context.Background(), o.ID, owner), o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	mux := http.NewServeMux()
	handlers.NewSettingsHandler(f.Deps, f.Projects, nil).Register(mux)
	const path = "/orgs/acme/projects/alpha/rename"
	member := addMember(t, f, o.ID, owner, org.RoleMember)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, path, "name=Hijacked", member))
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	got, err := f.Projects.ByID(setTenant(context.Background(), o.ID, owner), proj.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alpha", got.Name)

	admin := addMember(t, f, o.ID, owner, org.RoleAdmin)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, path, "name=Renamed", admin))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	got, err = f.Projects.ByID(setTenant(context.Background(), o.ID, owner), proj.ID)
	require.NoError(t, err)
	assert.Equal(t, "Renamed", got.Name)
}

func failManagerLookup(f *handlerFixture) int {
	before := f.OrgStore.MembershipOfCalls()
	f.OrgStore.MembershipOfFailAfter = before + 1
	f.OrgStore.MembershipOfErr = membershipLookupError{}
	return before
}

// SECURITY: a failed manager lookup refuses, even for the org owner.
func TestManagerGate_ALookupErrorRefuses(t *testing.T) {
	t.Parallel()
	t.Run("webhook delete", func(t *testing.T) {
		t.Parallel()
		x := newWebhookFixture(t)
		e := x.create(t)
		before := failManagerLookup(x.handlerFixture)

		rec := x.do(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/delete", nil)
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Equal(t, before+2, x.OrgStore.MembershipOfCalls(), "the scope gate and the manager lookup each read the membership once")
		x.OrgStore.MembershipOfErr = nil
		items, err := x.Hooks.List(x.ctx())
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})
	t.Run("device rules", func(t *testing.T) {
		t.Parallel()
		x := newDeviceFixture(t)
		d := x.seed(t, "Sales")
		before := failManagerLookup(x.handlerFixture)

		rec := x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/rules", "version=1&group_mode=open&trigger_prefix=%21bot", false)
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Equal(t, before+2, x.OrgStore.MembershipOfCalls(), "the scope gate and the manager lookup each read the membership once")
		got, err := x.Devices.Get(setTenantProject(context.Background(), x.org, x.project, x.uid), d.ID)
		require.NoError(t, err)
		assert.Empty(t, legacyRules(t, got.Device.Rules).TriggerPrefix)
	})
	t.Run("project rename", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		owner := uuid.New()
		o := f.seedOrg(t, "acme", owner)
		proj, err := f.Projects.Create(setTenant(context.Background(), o.ID, owner), o.ID, "alpha", "Alpha")
		require.NoError(t, err)
		mux := http.NewServeMux()
		handlers.NewSettingsHandler(f.Deps, f.Projects, nil).Register(mux)
		req := f.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/rename", "name=Renamed", session.Principal{UserID: owner, ActiveOrgID: o.ID})
		before := failManagerLookup(f)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Equal(t, before+2, f.OrgStore.MembershipOfCalls(), "the scope gate and the manager lookup each read the membership once")
		got, err := f.Projects.ByID(setTenant(context.Background(), o.ID, owner), proj.ID)
		require.NoError(t, err)
		assert.Equal(t, "Alpha", got.Name)
	})
}
