package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/webhook"
)

type deviceIDs struct{ svc *device.Service }

func (d deviceIDs) ResolveID(ctx context.Context, publicID string) (uuid.UUID, error) {
	dev, err := d.svc.Resolve(ctx, publicID)
	if device.IsNotFoundError(err) {
		return uuid.Nil, &webhook.DeviceNotFoundError{ID: publicID}
	}
	if err != nil {
		return uuid.Nil, err
	}
	return dev.ID, nil
}

func (x *webhookFixture) seedDevice(t *testing.T, projectID uuid.UUID, name string) *device.Device {
	t.Helper()
	d, err := x.Devices.Create(setTenantProject(context.Background(), x.org, projectID, x.uid), name)
	require.NoError(t, err)
	return d
}

func (x *webhookFixture) list(t *testing.T) []*webhook.Endpoint {
	t.Helper()
	got, err := x.Hooks.List(x.ctx())
	require.NoError(t, err)
	return got
}

func TestWebhookHandler_CreateScopedToANumber(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	d := x.seedDevice(t, x.project.ID, "Sales line")

	form := endpointForm("https://example.com/hook", string(events.MessageMatched))
	form.Set("device", d.PublicID)
	rec := x.do(t, http.MethodPost, webhookBase, form)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"), "the response carrying the secret is never cached")

	got := x.list(t)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].DeviceID)
	assert.Equal(t, d.ID, *got[0].DeviceID)

	list := x.do(t, http.MethodGet, webhookBase, nil)
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), "Sales line", "the list names the number")
	assert.NotContains(t, list.Body.String(), d.ID.String(), "the internal device id never reaches the page")
}

func TestWebhookHandler_CreateRefusesASiblingProjectsNumber(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	beta, err := x.Projects.Create(setTenant(context.Background(), x.org, x.uid), x.org, "beta", "Beta")
	require.NoError(t, err)
	sibling := x.seedDevice(t, beta.ID, "Beta line")

	form := endpointForm("https://example.com/hook", string(events.MessageMatched))
	form.Set("device", sibling.PublicID)
	rec := x.do(t, http.MethodPost, webhookBase, form)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "webhooks.error.number_not_found")
	assert.Empty(t, x.list(t), "nothing is created")
	assert.Empty(t, reSecret.FindString(rec.Body.String()))
}

func TestWebhookHandler_UpdateNeverMovesTheNumber(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	a := x.seedDevice(t, x.project.ID, "A")
	b := x.seedDevice(t, x.project.ID, "B")
	e, _, err := x.Hooks.Create(x.ctx(), "https://example.com/hook", "", []events.Type{events.MessageMatched}, a.PublicID)
	require.NoError(t, err)

	form := endpointForm("https://example.com/other", string(events.MessageMatched))
	form.Set("active", "1")
	form.Set("device", b.PublicID)
	rec := x.do(t, http.MethodPost, webhookBase+"/"+e.ID.String(), form)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	got, err := x.Hooks.ByID(x.ctx(), e.ID)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/other", got.URL)
	assert.Equal(t, a.ID, *got.DeviceID)

	detail := x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String(), nil)
	assert.Contains(t, detail.Body.String(), a.PublicID, "the detail page shows the number's public id")
	assert.NotContains(t, detail.Body.String(), `name="device"`, "the number is never an editable field after create")
}

func TestWebhookHandler_MemberDetailIsReadOnly(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	e := x.create(t)
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)

	rec := x.doAs(t, http.MethodGet, webhookBase+"/"+e.ID.String(), nil, member)
	require.Equal(t, http.StatusOK, rec.Code)
	main := mainOf(t, rec.Body.String())
	assert.NotContains(t, main, `name="url"`, "no settings form for a member")
	for _, suffix := range []string{"/rotate", "/retire", "/delete", "/test", "/retry-failed"} {
		assert.NotContains(t, main, e.ID.String()+suffix, "no %s action for a member", suffix)
	}
	assert.Contains(t, main, "https://example.com/hook", "values stay readable")
}

func TestWebhookHandler_RotateIsNoStoreAndMarksTheRotation(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	e := x.create(t)

	rec := x.do(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/rotate", url.Values{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	secret := reSecret.FindString(rec.Body.String())
	require.NotEmpty(t, secret)

	later := x.do(t, http.MethodGet, webhookBase+"/"+e.ID.String(), nil)
	assert.NotContains(t, later.Body.String(), secret)
	list := x.do(t, http.MethodGet, webhookBase, nil)
	assert.NotContains(t, list.Body.String(), secret)
}

func TestWebhookHandler_ListShowsTheLatestDelivery(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	e := x.create(t)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	x.settle(t, true)

	rec := x.do(t, http.MethodGet, webhookBase, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "webhooks.status.failed")
}

func TestWebhookHandler_RotateFromTheListReturnsOnlyTheSecretSlot(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	e := x.create(t)

	r := x.authedRequest(t, http.MethodPost, webhookBase+"/"+e.ID.String()+"/rotate", "", session.Principal{UserID: x.uid, ActiveOrgID: x.org})
	r.Header.Set("HX-Request", "true")
	r.Header.Set("HX-Target", "div#webhook-secret-slot")
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.NotEmpty(t, reSecret.FindString(rec.Body.String()))
	assert.NotContains(t, rec.Body.String(), "webhook-deliveries", "the list gets the secret slot, not the detail page")
}

func TestWebhookHandler_DetailShowsTheSecretTailOnly(t *testing.T) {
	t.Parallel()
	x := newWebhookFixture(t)
	created := x.do(t, http.MethodPost, webhookBase, endpointForm("https://example.com/hook", string(events.PostPublished)))
	secret := reSecret.FindString(created.Body.String())
	require.NotEmpty(t, secret)
	id := x.list(t)[0].ID.String()

	detail := x.do(t, http.MethodGet, webhookBase+"/"+id, nil)
	require.Equal(t, http.StatusOK, detail.Code)
	assert.Contains(t, detail.Body.String(), "whsec_••••"+secret[len(secret)-webhook.SecretHintLen:])
	assert.NotContains(t, detail.Body.String(), secret)
}
