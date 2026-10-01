package controlplane_test

import (
	"strings"
	"testing"

	projectv1connect "altalune.id/openwa/gen/go/project/v1/projectv1connect"
	"altalune.id/openwa/internal/controlplane"
	"altalune.id/openwa/internal/platform/authn"
)

// TestEveryRPCHasAScope guards the fail-closed rule: a mounted procedure with no scope entry is callable unchecked.
func TestEveryRPCHasAScope(t *testing.T) {
	srv := controlplane.New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_ = srv.Handler("")

	mounted := srv.MountedProcedures()
	if len(mounted) == 0 {
		t.Fatal("no procedures discovered; the test cannot guard anything")
	}

	table := controlplane.ScopeTable()
	for _, procedure := range mounted {
		t.Run(procedure, func(t *testing.T) {
			if _, ok := table[procedure]; !ok {
				t.Errorf("procedure %s has no scope entry in controlplane.ScopeTable()", procedure)
			}
		})
	}

	for procedure := range table {
		if !strings.HasPrefix(procedure, "/") {
			t.Errorf("scope table key %q must be a full procedure path starting with /", procedure)
		}
	}
}

func TestScopeTable_ProjectListRequiresProjectsRead(t *testing.T) {
	t.Parallel()
	if got := controlplane.ScopeTable()[projectv1connect.ProjectServiceListProjectsProcedure]; got != authn.ScopeProjectsRead {
		t.Fatalf("ListProjects scope = %q", got)
	}
}
