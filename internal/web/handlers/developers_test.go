package handlers_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/web/handlers"
)

const developersPath = "/orgs/acme/projects/alpha/developers"

func newDevelopersFixture(t *testing.T) *connectFixture {
	t.Helper()
	x := newConnectFixture(t)
	x.Mux = http.NewServeMux()
	handlers.NewDevelopersHandler(x.Deps, x.Projects, x.Keys, x.Devices, x.Hooks).Register(x.Mux)
	return x
}

func TestDevelopers_PagePrefillsBaseURLAndNumber(t *testing.T) {
	t.Parallel()
	x := newDevelopersFixture(t)
	rec := x.owned(t, http.MethodGet, developersPath, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "http://localhost/api/v1/orgs/acme/projects/alpha/devices/"+x.dev.PublicID+"/messages")
	assert.Empty(t, reKey.FindString(body), "the page never carries a real key")
	assert.Contains(t, body, "KEY=key_••••", "the masked key shows the configured prefix")
	assert.Contains(t, body, "developers.need_webhook", "no endpoint yet: the page says to add one")
}

func TestDevelopers_NumberSwitchStaysInTheProject(t *testing.T) {
	t.Parallel()
	x := newDevelopersFixture(t)
	other := x.seedDevice(t, x.alpha.ID, "Support")
	sibling := x.seedDevice(t, x.beta.ID, "Beta")

	rec := x.owned(t, http.MethodGet, developersPath+"?device="+other.PublicID, nil)
	assert.Contains(t, rec.Body.String(), "/devices/"+other.PublicID+"/messages")
	foreign := x.owned(t, http.MethodGet, developersPath+"?device="+sibling.PublicID, nil)
	assert.NotContains(t, foreign.Body.String(), sibling.PublicID, "a sibling project's number never reaches the page")
}

func TestDevelopers_SendATestNamesAnExistingEndpoint(t *testing.T) {
	t.Parallel()
	x := newDevelopersFixture(t)
	before := x.owned(t, http.MethodGet, developersPath, nil)
	assert.Contains(t, before.Body.String(), "developers.need_webhook")

	e, _, err := x.Hooks.Create(x.ctx(), "https://agent.example.com/hook", "", []events.Type{events.MessageMatched}, "")
	require.NoError(t, err)
	page := x.owned(t, http.MethodGet, developersPath, nil)
	assert.NotContains(t, page.Body.String(), "developers.need_webhook")
	assert.Contains(t, page.Body.String(), e.ID.String())
	assert.Empty(t, x.Outbox.Entries(), "opening the page sends nothing")
}
