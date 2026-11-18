package boot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/tenant"
)

func storedRules(t *testing.T, dsn string, id uuid.UUID) (string, int) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	var rules string
	var version int
	require.NoError(t, conn.QueryRow(context.Background(), "SELECT rules::text, version FROM openwa_devices WHERE id = $1", id).Scan(&rules, &version))
	return rules, version
}

// A malformed rules item is refused at the boundary and the stored rules stay byte-identical.
func TestDataPlane_MalformedRuleItemsLeaveTheRulesUnchanged(t *testing.T) {
	cfg := newSmokeCfg(t)
	f := newDataplaneFixtureFor(t, cfg, dataplaneOpts{enabled: true})
	projCtx := tenant.WithProject(f.orgCtx, f.project)
	d, err := f.srv.Devices.Create(projCtx, "Sales")
	require.NoError(t, err)
	_, key, err := f.srv.APIKeys.Mint(projCtx, "writer", []string{authn.ScopeDevicesRead, authn.ScopeDevicesWrite}, nil, soon())
	require.NoError(t, err)
	before, version := storedRules(t, cfg.DB.DSN, d.ID)

	bodies := map[string]string{
		"unknown item key":      `{"rules":{"items":[{"field":"x"}]}}`,
		"unknown rules key":     `{"rules":{"group_mod":"open"}}`,
		"unknown condition key": `{"rules":{"items":[{"id":"a","enabled":true,"action":"match","conditions":[{"field":"chat_kind","op":"is","values":["dm"],"extra":1}]}]}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, f.devicesURL+"/"+d.PublicID, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("If-Match", `W/"`+strconv.Itoa(version)+`"`)
			rec := httptest.NewRecorder()
			f.srv.Web.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"code":"bad_request"`, "the wire code stays bad_request")
			require.Regexp(t, `"message":"(items\[0\]|rules): unknown field \\"`, rec.Body.String(), "the message names where the key is and the key")
			after, afterVersion := storedRules(t, cfg.DB.DSN, d.ID)
			require.Equal(t, before, after, "the stored rules are byte-identical")
			require.Equal(t, version, afterVersion)
		})
	}
}
