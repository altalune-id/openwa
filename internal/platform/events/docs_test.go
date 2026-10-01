package events_test

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type goldenFence struct {
	file, name string
	line       int
	body       []byte
}

func goldenFences(t *testing.T, path string) []goldenFence {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // fixed docs path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []goldenFence
	var cur *goldenFence
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if cur == nil {
			if name, ok := strings.CutPrefix(line, "```json golden="); ok {
				cur = &goldenFence{file: path, name: strings.TrimSpace(name), line: n}
			}
			continue
		}
		if line == "```" {
			out = append(out, *cur)
			cur = nil
			continue
		}
		cur.body = append(append(cur.body, line...), '\n')
	}
	if cur != nil {
		t.Fatalf("%s:%d: golden fence %q never closes", path, cur.line, cur.name)
	}
	return out
}

func TestIntegrationDocsQuoteTheGoldens(t *testing.T) {
	docs, err := filepath.Glob(filepath.Join("..", "..", "..", "docs", "integration", "*.md"))
	if err != nil || len(docs) == 0 {
		t.Fatalf("no integration docs found: %v", err)
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		for _, f := range goldenFences(t, doc) {
			seen[f.name] = true
			want, err := os.ReadFile(filepath.Join("testdata", f.name+".golden.json")) //nolint:gosec // name comes from our own docs
			if err != nil {
				t.Errorf("%s:%d: fence names %q, which has no golden: %v", f.file, f.line, f.name, err)
				continue
			}
			if !bytes.Equal(f.body, want) {
				t.Errorf("%s:%d: fence %q drifted from testdata/%s.golden.json; paste the golden bytes.\ngot:\n%s\nwant:\n%s", f.file, f.line, f.name, f.name, f.body, want)
			}
		}
	}
	for _, name := range []string{"message_received_v1", "message_matched_v1", "message_status_v1"} {
		if !seen[name] {
			t.Errorf("no integration doc quotes golden %q; the guide must show every messaging payload", name)
		}
	}
}
