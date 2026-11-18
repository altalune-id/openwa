package handlers_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/authn"
)

func TestProjectAPIKeys_PresetDecidesTheScopes(t *testing.T) {
	cases := []struct {
		preset string
		posted []string
		want   []string
		access string
	}{
		{"send", []string{authn.ScopeDevicesWrite}, []string{authn.ScopeMessagesRead, authn.ScopeMessagesWrite}, "apikey.access_send"},
		{"read", []string{authn.ScopeMessagesWrite}, []string{authn.ScopeContactsRead, authn.ScopeMessagesRead}, "apikey.access_read"},
		{"custom", []string{authn.ScopeDevicesRead}, []string{authn.ScopeDevicesRead}, "apikey.access_custom"},
	}
	for _, tc := range cases {
		t.Run(tc.preset, func(t *testing.T) {
			x := newAPIKeyWebFixture(t)
			rec := x.as(t, x.owner, http.MethodPost, "/orgs/acme/projects/alpha/apikeys", url.Values{
				"name": {"bot"}, "preset": {tc.preset}, "scopes": tc.posted, "expires_in": {"30"},
			})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			keys, err := x.Keys.List(setTenantProject(t.Context(), x.org, x.alpha.ID, x.owner), x.alpha.ID)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			got := slices.Clone(keys[0].Scopes)
			slices.Sort(got)
			assert.Equal(t, tc.want, got, "a preset's scopes come from the server table, never the posted list")
			assert.Contains(t, rec.Body.String(), tc.access)
		})
	}
}

func TestProjectAPIKeys_PresetTilesOnlyForManagers(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	x.seedDevice(t, x.alpha.ID, "Sales", device.SessionStatus{})
	owner := x.as(t, x.owner, http.MethodGet, "/orgs/acme/projects/alpha/apikeys", nil)
	assert.Contains(t, owner.Body.String(), `name="preset"`)
	member := x.as(t, x.member, http.MethodGet, "/orgs/acme/projects/alpha/apikeys", nil)
	assert.NotContains(t, member.Body.String(), `name="preset"`)
}

func TestAPIKeys_MemberMintIsForbidden(t *testing.T) {
	for _, path := range []string{"/orgs/acme/projects/alpha/apikeys", "/orgs/acme/apikeys"} {
		t.Run(path, func(t *testing.T) {
			x := newAPIKeyWebFixture(t)
			form := url.Values{"name": {"bot"}, "preset": {"send"}, "scopes": {authn.ScopeDevicesRead}, "grant": {"all"}, "expires_in": {"30"}}

			rec := x.as(t, x.member, http.MethodPost, path, form)
			assert.Equal(t, http.StatusForbidden, rec.Code, "a member is refused, not shown a 200 error fragment")
			assert.Empty(t, reKey.FindString(rec.Body.String()))

			owner := x.as(t, x.owner, http.MethodPost, path, form)
			require.Equal(t, http.StatusOK, owner.Code, owner.Body.String())
			assert.NotEmpty(t, reKey.FindString(owner.Body.String()), "a manager still mints")
		})
	}
}

func TestAPIKeys_MemberKeyChangesAreForbidden(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	exp := time.Now().AddDate(0, 0, 30)
	projectKey, _, err := x.Keys.Mint(setTenantProject(t.Context(), x.org, x.alpha.ID, x.owner), "p", []string{authn.ScopeDevicesRead}, nil, &exp)
	require.NoError(t, err)
	orgKey, _, err := x.Keys.MintOrg(setTenant(t.Context(), x.org, x.owner), "o", []string{authn.ScopeDevicesRead}, apikey.ProjectGrant{All: true}, &exp)
	require.NoError(t, err)

	for _, target := range []string{
		"/orgs/acme/projects/alpha/apikeys/" + projectKey.ID.String() + "/revoke",
		"/orgs/acme/apikeys/" + orgKey.ID.String() + "/revoke",
		"/orgs/acme/apikeys/" + orgKey.ID.String() + "/projects",
		"/orgs/acme/apikeys/" + orgKey.ID.String() + "/all-projects",
	} {
		rec := x.as(t, x.member, http.MethodPost, target, url.Values{"project_ids": {x.alpha.ID.String()}})
		assert.Equal(t, http.StatusForbidden, rec.Code, target)
	}
	for _, k := range x.Store.All() {
		assert.Nil(t, k.RevokedAt, "a member's revoke never lands")
	}
}
