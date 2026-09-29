package schema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	pcfg "altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/testutil/pgtest"
)

func TestCheckRLSGuard_NotBypass_OK(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	mock.ExpectQuery(`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		WillReturnRows(sqlmock.NewRows([]string{"rolbypassrls"}).AddRow(false))
	if err := CheckRLSGuard(context.Background(), conn, false); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestCheckRLSGuard_Bypass_NotAllowed(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	mock.ExpectQuery(`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		WillReturnRows(sqlmock.NewRows([]string{"rolbypassrls"}).AddRow(true))
	err = CheckRLSGuard(context.Background(), conn, false)
	if !errors.Is(err, ErrRLSBypass) {
		t.Errorf("err=%v want ErrRLSBypass", err)
	}
}

func TestCheckRLSGuard_Bypass_AllowedInDev(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	mock.ExpectQuery(`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		WillReturnRows(sqlmock.NewRows([]string{"rolbypassrls"}).AddRow(true))
	if err := CheckRLSGuard(context.Background(), conn, true); err != nil {
		t.Errorf("expected nil with allowBypass=true, got %v", err)
	}
}

func TestCheckRLSGuard_QueryError_Wraps(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	sentinel := fmt.Errorf("connection lost")
	mock.ExpectQuery(`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		WillReturnError(sentinel)
	err = CheckRLSGuard(context.Background(), conn, false)
	if err == nil || !errors.Is(err, sentinel) {
		t.Errorf("err=%v want wraps connection lost", err)
	}
}

func TestRLSGuard_NilConfig(t *testing.T) {
	if err := RLSGuard(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestRLSGuard_AllowBypass_SkipsChecks(t *testing.T) {
	conn, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	cfg := &pcfg.Config{DB: db.DBConfig{AllowBypassRLS: true}}
	if err := RLSGuard(context.Background(), conn, cfg); err != nil {
		t.Errorf("expected nil with AllowBypassRLS=true, got %v", err)
	}
}

func TestRLSAuditError_Error_IncludesCounts(t *testing.T) {
	e := &RLSAuditError{
		MissingRLS:    []string{"a"},
		MissingForce:  []string{"a", "b"},
		MissingPolicy: []string{"a", "b", "c"},
	}
	msg := e.Error()
	if !strings.Contains(msg, "1 missing RLS") ||
		!strings.Contains(msg, "2 missing FORCE") ||
		!strings.Contains(msg, "3 missing tenant policy") {
		t.Errorf("Error() = %q; want counts 1/2/3", msg)
	}
}

func TestIsRLSAuditError_UnwrapsThroughFmt(t *testing.T) {
	inner := &RLSAuditError{MissingRLS: []string{"openwa_todos"}}
	wrapped := fmt.Errorf("boot: %w", inner)
	if !IsRLSAuditError(wrapped) {
		t.Errorf("IsRLSAuditError(wrapped) = false; want true")
	}
	if IsRLSAuditError(errors.New("other")) {
		t.Errorf("IsRLSAuditError(other) = true; want false")
	}
}

func TestAuditPolicies_Empty_NoOp(t *testing.T) {
	if err := AuditPolicies(context.Background(), nil, nil); err != nil {
		t.Errorf("expected nil for empty tables, got %v", err)
	}
}

type passthroughConverter struct{}

func (passthroughConverter) ConvertValue(v any) (driver.Value, error) { return v, nil }

func TestPolicyPosture_Observe(t *testing.T) {
	markers := []string{currentOrgIDGUC, "openwa_current_org_id("}
	const scopedQual = "(org_id = current_setting('app.current_org_id')::uuid)"
	const helperQual = "(org_id = openwa_current_org_id())"

	tests := []struct {
		name             string
		rows             []policyRow
		wantScopedRead   bool
		wantUnscoped     bool
		wantWriteCovered bool
	}{
		{
			name:             "for all using only",
			rows:             []policyRow{{qual: scopedQual, cmd: "ALL"}},
			wantScopedRead:   true,
			wantWriteCovered: true,
		},
		{
			name:             "helper scoped qual",
			rows:             []policyRow{{qual: helperQual, cmd: "ALL"}},
			wantScopedRead:   true,
			wantWriteCovered: true,
		},
		{
			name:             "explicit scoped with check",
			rows:             []policyRow{{qual: scopedQual, withCheck: scopedQual, cmd: "ALL"}},
			wantScopedRead:   true,
			wantWriteCovered: true,
		},
		{
			name:             "with check true",
			rows:             []policyRow{{qual: scopedQual, withCheck: "true", cmd: "ALL"}},
			wantScopedRead:   true,
			wantUnscoped:     true,
			wantWriteCovered: true,
		},
		{
			name:           "select only",
			rows:           []policyRow{{qual: scopedQual, cmd: "SELECT"}},
			wantScopedRead: true,
		},
		{
			name:             "insert only with scoped check",
			rows:             []policyRow{{withCheck: scopedQual, cmd: "INSERT"}},
			wantWriteCovered: true,
		},
		{
			name:             "insert only with unscoped check",
			rows:             []policyRow{{withCheck: "true", cmd: "INSERT"}},
			wantUnscoped:     true,
			wantWriteCovered: true,
		},
		{
			name:             "unscoped qual",
			rows:             []policyRow{{qual: "true", cmd: "ALL"}},
			wantUnscoped:     true,
			wantWriteCovered: true,
		},
		{
			name:             "no expression at all",
			rows:             []policyRow{{cmd: "ALL"}},
			wantUnscoped:     true,
			wantWriteCovered: true,
		},
		{
			name: "select and insert split",
			rows: []policyRow{
				{qual: scopedQual, cmd: "SELECT"},
				{withCheck: scopedQual, cmd: "INSERT"},
			},
			wantScopedRead:   true,
			wantWriteCovered: true,
		},
		{
			name: "good policy does not mask an unscoped sibling",
			rows: []policyRow{
				{qual: scopedQual, cmd: "ALL"},
				{qual: "true", cmd: "SELECT"},
			},
			wantScopedRead:   true,
			wantUnscoped:     true,
			wantWriteCovered: true,
		},
		{
			name: "unscoped sibling seen before the good policy",
			rows: []policyRow{
				{qual: scopedQual, withCheck: "true", cmd: "UPDATE"},
				{qual: scopedQual, cmd: "ALL"},
			},
			wantScopedRead:   true,
			wantUnscoped:     true,
			wantWriteCovered: true,
		},
		{
			name:             "lowercase cmd still covers writes",
			rows:             []policyRow{{qual: scopedQual, cmd: "delete"}},
			wantScopedRead:   true,
			wantWriteCovered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got policyPosture
			for _, r := range tt.rows {
				got.observe(r, markers)
			}
			if got.scopedRead != tt.wantScopedRead {
				t.Errorf("scopedRead = %v; want %v", got.scopedRead, tt.wantScopedRead)
			}
			if got.unscoped != tt.wantUnscoped {
				t.Errorf("unscoped = %v; want %v", got.unscoped, tt.wantUnscoped)
			}
			if got.writeCovered != tt.wantWriteCovered {
				t.Errorf("writeCovered = %v; want %v", got.writeCovered, tt.wantWriteCovered)
			}
		})
	}
}

func newAuditMock(t *testing.T, policies *sqlmock.Rows) *sql.DB {
	t.Helper()
	conn, mock, err := sqlmock.New(sqlmock.ValueConverterOption(passthroughConverter{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	mock.ExpectQuery(`FROM pg_class`).WillReturnRows(
		sqlmock.NewRows([]string{"relname", "relrowsecurity", "relforcerowsecurity"}).
			AddRow(auditMockTable, true, true))
	mock.ExpectQuery(`FROM pg_proc`).WillReturnRows(sqlmock.NewRows([]string{"marker"}))
	mock.ExpectQuery(`FROM pg_policies`).WillReturnRows(policies)
	return conn
}

const auditMockTable = "openwa_todos"

func auditPolicyRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"tablename", "qual", "with_check", "cmd"})
}

func TestAuditPolicies_ForAllUsingOnly_Passes(t *testing.T) {
	rows := auditPolicyRows().AddRow(auditMockTable, "(org_id = current_setting('app.current_org_id')::uuid)", "", "ALL")
	if err := AuditPolicies(context.Background(), newAuditMock(t, rows), []string{auditMockTable}); err != nil {
		t.Fatalf("AuditPolicies = %v; want nil", err)
	}
}

func TestAuditPolicies_ExplicitWithCheckTrue_Fails(t *testing.T) {
	rows := auditPolicyRows().AddRow(auditMockTable, "(org_id = current_setting('app.current_org_id')::uuid)", "true", "ALL")
	err := AuditPolicies(context.Background(), newAuditMock(t, rows), []string{auditMockTable})
	var audit *RLSAuditError
	if !errors.As(err, &audit) {
		t.Fatalf("AuditPolicies = %v; want *RLSAuditError", err)
	}
	if !slices.Contains(audit.UnscopedPolicy, auditMockTable) {
		t.Errorf("UnscopedPolicy = %v; want to include %q", audit.UnscopedPolicy, auditMockTable)
	}
}

func TestAuditPolicies_SelectOnlyPolicy_Fails(t *testing.T) {
	rows := auditPolicyRows().AddRow(auditMockTable, "(org_id = current_setting('app.current_org_id')::uuid)", "", "SELECT")
	err := AuditPolicies(context.Background(), newAuditMock(t, rows), []string{auditMockTable})
	var audit *RLSAuditError
	if !errors.As(err, &audit) {
		t.Fatalf("AuditPolicies = %v; want *RLSAuditError", err)
	}
	if !slices.Contains(audit.MissingWritePolicy, auditMockTable) {
		t.Errorf("MissingWritePolicy = %v; want to include %q", audit.MissingWritePolicy, auditMockTable)
	}
}

func TestAuditPolicies_UnscopedSiblingPolicy_Fails(t *testing.T) {
	const scoped = "(org_id = current_setting('app.current_org_id')::uuid)"
	rows := auditPolicyRows().
		AddRow(auditMockTable, scoped, "", "ALL").
		AddRow(auditMockTable, "true", "", "SELECT")
	err := AuditPolicies(context.Background(), newAuditMock(t, rows), []string{auditMockTable})
	var audit *RLSAuditError
	if !errors.As(err, &audit) {
		t.Fatalf("AuditPolicies = %v; want *RLSAuditError", err)
	}
	if !slices.Contains(audit.UnscopedPolicy, auditMockTable) {
		t.Errorf("UnscopedPolicy = %v; want to include %q", audit.UnscopedPolicy, auditMockTable)
	}
	if len(audit.MissingPolicy) != 0 {
		t.Errorf("MissingPolicy = %v; want empty", audit.MissingPolicy)
	}
}

func TestAuditPolicies_SplitSelectAndInsertPolicies_Passes(t *testing.T) {
	const scoped = "(org_id = current_setting('app.current_org_id')::uuid)"
	rows := auditPolicyRows().
		AddRow(auditMockTable, scoped, "", "SELECT").
		AddRow(auditMockTable, "", scoped, "INSERT")
	if err := AuditPolicies(context.Background(), newAuditMock(t, rows), []string{auditMockTable}); err != nil {
		t.Fatalf("AuditPolicies = %v; want nil", err)
	}
}

func TestRLSGuard_Integration(t *testing.T) {
	conn := pgtest.New(t).OpenDB(t)

	ctx := context.Background()
	const table = "openwa_todos"

	var bypassRLS bool
	if err := conn.QueryRowContext(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`,
	).Scan(&bypassRLS); err != nil {
		t.Fatalf("probe current_user: %v", err)
	}

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE openwa_todos (
			id     UUID PRIMARY KEY,
			org_id UUID NOT NULL,
			title  TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE openwa_todos ENABLE ROW LEVEL SECURITY`); err != nil {
		t.Fatalf("enable rls: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE openwa_todos FORCE ROW LEVEL SECURITY`); err != nil {
		t.Fatalf("force rls: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE POLICY openwa_todos_tenant ON openwa_todos
		USING (org_id = current_setting('app.current_org_id')::uuid)
	`); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	cfg := &pcfg.Config{
		DB: db.DBConfig{
			AllowBypassRLS: bypassRLS,
		},
		Tenant: pcfg.TenantConfig{
			TenantScopedTables: []string{table},
		},
	}

	if bypassRLS {
		if err := AuditPolicies(ctx, conn, []string{table}); err != nil {
			t.Fatalf("AuditPolicies passing case: %v", err)
		}
	} else {
		if err := RLSGuard(ctx, conn, cfg); err != nil {
			t.Fatalf("RLSGuard passing case: %v", err)
		}
	}

	if _, err := conn.ExecContext(ctx, `DROP POLICY openwa_todos_tenant ON openwa_todos`); err != nil {
		t.Fatalf("drop policy: %v", err)
	}

	var auditErr error
	if bypassRLS {
		auditErr = AuditPolicies(ctx, conn, []string{table})
	} else {
		auditErr = RLSGuard(ctx, conn, cfg)
	}
	if auditErr == nil {
		t.Fatal("expected error after policy dropped, got nil")
	}
	if !IsRLSAuditError(auditErr) {
		t.Fatalf("err = %v; want *RLSAuditError", auditErr)
	}
	var audit *RLSAuditError
	if !errors.As(auditErr, &audit) {
		t.Fatalf("errors.As(*RLSAuditError): failed for %v", auditErr)
	}
	if !slices.Contains(audit.MissingPolicy, table) {
		t.Errorf("MissingPolicy = %v; want to include %q", audit.MissingPolicy, table)
	}
}

func TestAuditPolicies_AcceptsMigratedHelperScopedPolicies(t *testing.T) {
	h := pgtest.New(t)
	conn := h.OpenDB(t)

	cfg := pcfg.Defaults()
	cfg.DB.Schema = h.Schema
	if err := MigrateUp(t.Context(), conn, cfg); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	if err := AuditPolicies(t.Context(), conn, TenantTableNames(cfg.DB.TablePrefix)); err != nil {
		t.Fatalf("AuditPolicies rejected the policies migration 002 creates: %v", err)
	}
}

func TestAuditPolicies_PolicyShapes(t *testing.T) {
	const scoped = `(org_id = current_setting('app.current_org_id')::uuid)`

	tests := []struct {
		name    string
		policy  string
		wantErr bool
		bucket  func(*RLSAuditError) []string
	}{
		{
			name:   "for all using only passes",
			policy: `CREATE POLICY openwa_todos_tenant ON openwa_todos USING ` + scoped,
		},
		{
			name:   "for all with an explicitly scoped with check passes",
			policy: `CREATE POLICY openwa_todos_tenant ON openwa_todos USING ` + scoped + ` WITH CHECK ` + scoped,
		},
		{
			name:    "explicit with check true fails",
			policy:  `CREATE POLICY openwa_todos_tenant ON openwa_todos USING ` + scoped + ` WITH CHECK (true)`,
			wantErr: true,
			bucket:  func(e *RLSAuditError) []string { return e.UnscopedPolicy },
		},
		{
			name:    "for select only fails",
			policy:  `CREATE POLICY openwa_todos_tenant ON openwa_todos FOR SELECT USING ` + scoped,
			wantErr: true,
			bucket:  func(e *RLSAuditError) []string { return e.MissingWritePolicy },
		},
		{
			name: "unscoped sibling policy fails",
			policy: `CREATE POLICY openwa_todos_tenant ON openwa_todos USING ` + scoped + `;
				CREATE POLICY openwa_todos_open ON openwa_todos FOR SELECT USING (true)`,
			wantErr: true,
			bucket:  func(e *RLSAuditError) []string { return e.UnscopedPolicy },
		},
	}

	// NOTE: one container for every case — pgtest.New starts a Postgres per call, and a container
	// per subtest pushed this package past its 10 minute timeout.
	conn := pgtest.New(t).OpenDB(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS openwa_todos CASCADE`); err != nil {
				t.Fatalf("drop table: %v", err)
			}
			if _, err := conn.ExecContext(ctx, `
				CREATE TABLE openwa_todos (
					id     UUID PRIMARY KEY,
					org_id UUID NOT NULL,
					title  TEXT NOT NULL
				);
				ALTER TABLE openwa_todos ENABLE ROW LEVEL SECURITY;
				ALTER TABLE openwa_todos FORCE ROW LEVEL SECURITY;
			`); err != nil {
				t.Fatalf("create table: %v", err)
			}
			if _, err := conn.ExecContext(ctx, tt.policy); err != nil {
				t.Fatalf("create policy: %v", err)
			}

			err := AuditPolicies(ctx, conn, []string{"openwa_todos"})
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("AuditPolicies = %v; want nil", err)
				}
				return
			}
			var audit *RLSAuditError
			if !errors.As(err, &audit) {
				t.Fatalf("AuditPolicies = %v; want *RLSAuditError", err)
			}
			if got := tt.bucket(audit); !slices.Contains(got, "openwa_todos") {
				t.Errorf("bucket = %v; want to include %q (full audit: %+v)", got, "openwa_todos", audit)
			}
		})
	}
}
