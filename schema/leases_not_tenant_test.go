package schema

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	leasesEnableRLSRe = regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(?:{{\.Schema}}\.)?{{\.TablePrefix}}whatsapp_leases\s+(?:NO\s+)?(?:ENABLE|FORCE)\s+ROW\s+LEVEL\s+SECURITY`)
	leasesPolicyRe    = regexp.MustCompile(`(?is)CREATE\s+POLICY\s+\S+\s+ON\s+(?:{{\.Schema}}\.)?{{\.TablePrefix}}whatsapp_leases`)
)

const leasesRLSConsequence = "the whatsapp runtime claims leases across every tenant before any tenant scope exists, so an RLS policy on whatsapp_leases makes every Claim read as empty and no device ever connects"

// TestTenantTableSuffixes_OmitsWhatsappLeases pins whatsapp_leases out of the RLS-managed table list and into RequiredTableSuffixes.
func TestTenantTableSuffixes_OmitsWhatsappLeases(t *testing.T) {
	if slices.Contains(TenantTableSuffixes, "whatsapp_leases") {
		t.Fatalf("TenantTableSuffixes contains %q: %s", "whatsapp_leases", leasesRLSConsequence)
	}
	if !slices.Contains(RequiredTableSuffixes, "whatsapp_leases") {
		t.Fatalf("RequiredTableSuffixes omits %q; boot would not notice the table missing", "whatsapp_leases")
	}
}

// TestPostgresMigrations_NeverEnableRLSOnWhatsappLeases catches an RLS statement added later.
func TestPostgresMigrations_NeverEnableRLSOnWhatsappLeases(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations/postgres")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	seen := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		seen++
		src := readPostgresMigration(t, e.Name())
		for _, re := range []*regexp.Regexp{leasesEnableRLSRe, leasesPolicyRe} {
			if m := re.FindString(src); m != "" {
				t.Errorf("%s contains %q: %s", e.Name(), strings.Join(strings.Fields(m), " "), leasesRLSConsequence)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no postgres migrations found; the guard would pass vacuously")
	}
}
