package schema

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	mailQueueEnableRLSRe = regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(?:{{\.Schema}}\.)?{{\.TablePrefix}}mail_queue\s+(?:NO\s+)?(?:ENABLE|FORCE)\s+ROW\s+LEVEL\s+SECURITY`)
	mailQueuePolicyRe    = regexp.MustCompile(`(?is)CREATE\s+POLICY\s+\S+\s+ON\s+(?:{{\.Schema}}\.)?{{\.TablePrefix}}mail_queue`)
)

const mailQueueRLSConsequence = "the mail worker claims due mail across every tenant before any tenant scope exists, so an RLS policy on mail_queue makes every claim read as empty and no mail is ever sent"

// TestTenantTableSuffixes_OmitsMailQueue pins mail_queue out of the RLS-managed table list and into RequiredTableSuffixes.
func TestTenantTableSuffixes_OmitsMailQueue(t *testing.T) {
	if slices.Contains(TenantTableSuffixes, "mail_queue") {
		t.Fatalf("TenantTableSuffixes contains %q: %s", "mail_queue", mailQueueRLSConsequence)
	}
	if !slices.Contains(RequiredTableSuffixes, "mail_queue") {
		t.Fatalf("RequiredTableSuffixes omits %q; boot would not notice the table missing", "mail_queue")
	}
}

// TestPostgresMigrations_NeverEnableRLSOnMailQueue catches an RLS statement added later.
func TestPostgresMigrations_NeverEnableRLSOnMailQueue(t *testing.T) {
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
		for _, re := range []*regexp.Regexp{mailQueueEnableRLSRe, mailQueuePolicyRe} {
			if m := re.FindString(src); m != "" {
				t.Errorf("%s contains %q: %s", e.Name(), strings.Join(strings.Fields(m), " "), mailQueueRLSConsequence)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no postgres migrations found; the guard would pass vacuously")
	}
}
