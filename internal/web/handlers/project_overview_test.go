package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
	"altalune.id/openwa/internal/webhook"
)

const overviewBase = "/orgs/acme/projects/alpha"

type overviewFixture struct {
	*handlerFixture
	Mux      *http.ServeMux
	Devices  *device.Service
	Sessions *fakes.DeviceSessions
	Messages *fakes.Message
	Hooks    *fakes.WebhookStore
	now      time.Time
	uid      uuid.UUID
	org      uuid.UUID
	project  uuid.UUID
}

func newOverviewFixture(t *testing.T) *overviewFixture {
	t.Helper()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	proj, err := f.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)

	x := &overviewFixture{handlerFixture: f, Sessions: fakes.NewDeviceSessions(), Messages: fakes.NewMessage(), Hooks: fakes.NewWebhookStore(),
		now: time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC), uid: uid, org: o.ID, project: proj.ID}
	x.Devices = device.NewService(fakes.NewDevice(), discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, x.Sessions, "")
	msgs := message.NewService(x.Messages, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, message.Deps{},
		message.Options{Clock: func() time.Time { return x.now }})
	hooks := webhook.NewService(x.Hooks, discardLogger(), passthroughUnexpected(), nil, fakes.NewOutbox(), slugOf{})
	x.Mux = http.NewServeMux()
	handlers.NewProjectOverviewHandler(f.Deps, f.Projects, x.Devices, msgs, hooks).Register(x.Mux)
	return x
}

func (x *overviewFixture) ctx() context.Context {
	return setTenantProject(context.Background(), x.org, x.project, x.uid)
}

func (x *overviewFixture) home(t *testing.T, p session.Principal, query string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, x.authedRequest(t, http.MethodGet, overviewBase+"/overview"+query, "", p))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return mainOf(t, rec.Body.String())
}

func (x *overviewFixture) owner() session.Principal {
	return session.Principal{UserID: x.uid, ActiveOrgID: x.org, ActiveProjectID: x.project}
}

func (x *overviewFixture) number(t *testing.T, name string, st device.SessionStatus) *device.Device {
	t.Helper()
	d, err := x.Devices.Create(x.ctx(), name)
	require.NoError(t, err)
	x.Sessions.Statuses[d.ID] = st
	return d
}

func (x *overviewFixture) message(projectID, deviceID uuid.UUID, at time.Time, outcome message.MatchOutcome) {
	x.Messages.Seed(&message.Message{ID: uuid.New(), PublicID: fakes.MessagePublicID(), OrgID: x.org, ProjectID: projectID, DeviceID: deviceID, ChatID: uuid.New(),
		Direction: message.DirectionIn, Type: message.TypeText, Status: message.StatusReceived, WATimestamp: at, MatchOutcome: outcome})
}

func (x *overviewFixture) webhook(active bool, types ...events.Type) {
	x.Hooks.Seed(&webhook.Endpoint{ID: uuid.New(), OrgID: x.org, ProjectID: x.project, URL: "https://agent.example/hook", EventTypes: types, Active: active})
}

var doneStep = regexp.MustCompile(`data-step-done`)

func TestHome_NewProjectShowsTheChecklistAndTheEmptyNumbers(t *testing.T) {
	x := newOverviewFixture(t)
	body := x.home(t, x.owner(), "")
	require.Contains(t, body, "home.no_numbers_title")
	require.Contains(t, body, `data-tour="connect"`, "the first step carries the Home tip target")
	require.Contains(t, body, `action="`+overviewBase+`/devices"`, "Connect WhatsApp creates and pairs in one request")
	require.Contains(t, body, `value="0" max="4"`)
	require.Empty(t, doneStep.FindAllString(body, -1))
	require.NotContains(t, body, "settings.project_uuid", "IDs live in Settings, not on Home")
}

func TestHome_ChecklistStepsComeFromData(t *testing.T) {
	seen := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		setup func(t *testing.T, x *overviewFixture)
		done  int
	}{
		"a number that was never linked": {func(t *testing.T, x *overviewFixture) {
			x.number(t, "WhatsApp", device.SessionStatus{State: device.SessionLinking})
		}, 0},
		"a linked number, default rules": {func(t *testing.T, x *overviewFixture) {
			x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
		}, 1},
		"a number signed out after it was linked": {func(t *testing.T, x *overviewFixture) {
			x.number(t, "Sales", device.SessionStatus{State: device.SessionLoggedOut, LastConnectedAt: &seen})
		}, 1},
		"rules changed from the defaults": {func(t *testing.T, x *overviewFixture) {
			d := x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
			r := device.FromLegacy(device.Legacy{GroupMode: device.GroupOpen, IgnoreFromMe: true})
			_, err := x.Devices.Update(x.ctx(), d.ID, device.Update{Rules: &r}, d.Version)
			require.NoError(t, err)
		}, 2},
		"every rule deleted": {func(t *testing.T, x *overviewFixture) {
			d := x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
			r := device.Rules{IgnoreFromMe: true}
			_, err := x.Devices.Update(x.ctx(), d.ID, device.Update{Rules: &r}, d.Version)
			require.NoError(t, err)
		}, 1},
		"changed rules that send nothing to the agent": {func(t *testing.T, x *overviewFixture) {
			d := x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
			r := device.DefaultRules()
			r.Items[0].Action = device.ActionIgnore
			r.Items[1].Enabled = false
			_, err := x.Devices.Update(x.ctx(), d.ID, device.Update{Rules: &r}, d.Version)
			require.NoError(t, err)
		}, 1},
		"an enabled message.matched webhook": {func(t *testing.T, x *overviewFixture) {
			x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
			x.webhook(true, events.MessageMatched)
		}, 2},
		"a disabled or other-event webhook": {func(t *testing.T, x *overviewFixture) {
			x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
			x.webhook(false, events.MessageMatched)
			x.webhook(true, events.MessageReceived)
		}, 1},
		"a message sent to the agent": {func(t *testing.T, x *overviewFixture) {
			d := x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
			x.message(x.project, d.ID, x.now.Add(-time.Hour), message.MatchMatched)
		}, 2},
		"only a sibling project's message was sent": {func(t *testing.T, x *overviewFixture) {
			x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
			x.message(uuid.New(), uuid.New(), x.now.Add(-time.Hour), message.MatchMatched)
		}, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			x := newOverviewFixture(t)
			c.setup(t, x)
			body := x.home(t, x.owner(), "")
			require.Len(t, doneStep.FindAllString(body, -1), c.done)
		})
	}
}

func TestHome_AllStepsDoneShowsTheBannerAndCanReopen(t *testing.T) {
	x := newOverviewFixture(t)
	d := x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
	r := device.FromLegacy(device.Legacy{GroupMode: device.GroupOpen, IgnoreFromMe: true})
	_, err := x.Devices.Update(x.ctx(), d.ID, device.Update{Rules: &r}, d.Version)
	require.NoError(t, err)
	x.webhook(true, events.MessageMatched)
	x.message(x.project, d.ID, x.now.Add(-time.Hour), message.MatchMatched)

	body := x.home(t, x.owner(), "")
	require.Contains(t, body, "home.done")
	require.NotContains(t, body, "home.setup_title")
	require.Contains(t, body, `href="`+overviewBase+`/overview?checklist=open"`)

	body = x.home(t, x.owner(), "?checklist=open")
	require.Len(t, doneStep.FindAllString(body, -1), 4)
}

func TestHome_NumbersShowStatusLastMessageAndTheLast24Hours(t *testing.T) {
	x := newOverviewFixture(t)
	ok := x.number(t, "Sales", device.SessionStatus{State: device.SessionConnected, Phone: "628111"})
	out := x.number(t, "Gudang", device.SessionStatus{State: device.SessionLoggedOut, Phone: "628222"})
	x.message(x.project, ok.ID, x.now.Add(-time.Hour), message.MatchNone)
	x.message(x.project, ok.ID, x.now.Add(-23*time.Hour), message.MatchNone)
	x.message(x.project, ok.ID, x.now.Add(-25*time.Hour), message.MatchNone)
	x.message(uuid.New(), ok.ID, x.now.Add(-time.Hour), message.MatchNone)

	body := x.home(t, x.owner(), "")
	row := func(id string) string {
		i := strings.Index(body, `<tr class="border-t border-base-300"><td class="px-4 py-2"><a href="`+overviewBase+`/devices/`+id+`"`)
		require.GreaterOrEqual(t, i, 0, "a table row for %s", id)
		j := strings.Index(body[i:], "</tr>")
		return body[i : i+j]
	}
	sales := row(ok.PublicID)
	require.Contains(t, sales, "devices.state.connected")
	require.Regexp(t, `text-end tabular-nums">2<`, sales, "two in the last 24 hours; older and sibling-project rows are not counted")
	require.Contains(t, sales, "<time ")
	gudang := row(out.PublicID)
	require.Contains(t, gudang, "devices.state.logged_out")
	require.Contains(t, gudang, "home.fix_it")
	require.Contains(t, gudang, "home.no_messages")

	phone := body[strings.Index(body, `<ul class="list rounded-box border border-base-300 bg-base-100 lg:hidden">`):]
	phone = phone[:strings.Index(phone, "</ul>")]
	gudangRow := phone[strings.Index(phone, out.PublicID):]
	require.Contains(t, gudangRow[:strings.Index(gudangRow, "</li>")], "home.fix_it", "the phone list offers Fix it like the table")
	salesRow := phone[strings.Index(phone, ok.PublicID):]
	require.Contains(t, salesRow[:strings.Index(salesRow, "</li>")], "common.open")
	require.Regexp(t, `home\.last_message\s+<time `, salesRow, "whitespace sits between the label and the time")
}

func TestHome_AMemberSeesNoWriteActions(t *testing.T) {
	x := newOverviewFixture(t)
	x.number(t, "Sales", device.SessionStatus{State: device.SessionLoggedOut, Phone: "628111"})
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)
	member.ActiveProjectID = x.project

	body := x.home(t, member, "")
	require.NotContains(t, body, `action="`+overviewBase+`/devices"`)
	require.NotContains(t, body, "home.fix_it")
	require.NotContains(t, body, `/webhooks/new"`)
	require.Contains(t, body, managersOnlyKey)
	require.Contains(t, body, `href="`+overviewBase+`/inbox"`, "a member can still open the Inbox")
}
