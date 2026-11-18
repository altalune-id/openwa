package schema_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebhookDeviceMigration_ScopesEndpointToProjectDevice(t *testing.T) {
	sqlDB, sch, pfx := migrated(t)
	tbl := func(name string) string { return sch + "." + pfx + name }
	exec := func(q string, args ...any) error {
		_, err := sqlDB.ExecContext(t.Context(), q, args...)
		return err
	}
	const (
		user     = "00000000-0000-0000-0000-000000000001"
		orgID    = "00000000-0000-0000-0000-0000000000a1"
		projA    = "00000000-0000-0000-0000-0000000000b1"
		projB    = "00000000-0000-0000-0000-0000000000b2"
		deviceA  = "00000000-0000-0000-0000-0000000000d1"
		scoped   = "00000000-0000-0000-0000-0000000000e1"
		wide     = "00000000-0000-0000-0000-0000000000e2"
		sibling  = "00000000-0000-0000-0000-0000000000e3"
		endpoint = `INSERT INTO %s (id, org_id, project_id, url, event_types, secret_primary, active, created_at, updated_at, device_id) VALUES ($1, $2, $3, 'https://x.example', 'message.matched', '\x01', true, now(), now(), $4)`
	)
	require.NoError(t, exec(`INSERT INTO `+tbl("users")+` (id, email, created_at, updated_at) VALUES ($1, 'u@x.co', now(), now())`, user))
	require.NoError(t, exec(`INSERT INTO `+tbl("orgs")+` (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'o', 'O', $2, now(), now())`, orgID, user))
	for i, p := range []string{projA, projB} {
		require.NoError(t, exec(`INSERT INTO `+tbl("projects")+` (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'P', $4, now(), now())`, p, orgID, []string{"a", "b"}[i], user))
	}
	require.NoError(t, exec(`INSERT INTO `+tbl("devices")+` (id, public_id, org_id, project_id, name, created_at, updated_at) VALUES ($1, 'dev_a', $2, $3, 'A', now(), now())`, deviceA, orgID, projA))

	ins := func(id, project string, device any) error {
		return exec(fmt.Sprintf(endpoint, tbl("webhook_endpoints")), id, orgID, project, device)
	}
	require.NoError(t, ins(scoped, projA, deviceA))
	require.NoError(t, ins(wide, projA, nil))
	err := ins(sibling, projB, deviceA)
	require.ErrorContains(t, err, pfx+"webhook_endpoints_device_fk", "a device of a sibling project must be refused")

	require.NoError(t, exec(`DELETE FROM `+tbl("devices")+` WHERE id = $1`, deviceA))
	var left []string
	rows, err := sqlDB.QueryContext(t.Context(), `SELECT id::text FROM `+tbl("webhook_endpoints")+` ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		left = append(left, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{wide}, left, "deleting a device removes only its scoped endpoints")
}
