package boot

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWebHandler_InstallsTenantMiddlewareAfterSession proves r.Context() carries a tenant scope without each handler calling tenant.Into.
func TestWebHandler_InstallsTenantMiddlewareAfterSession(t *testing.T) {
	src, err := os.ReadFile("http.go")
	require.NoError(t, err)
	source := string(src)

	structStart := strings.Index(source, "func surfaceChains(")
	require.Positive(t, structStart, "surfaceChains func not found — this guard needs updating")
	structEnd := strings.Index(source[structStart:], "\n\t}\n}")
	require.Positive(t, structEnd, "surface chains struct end not found — this guard needs updating")
	chains := source[structStart : structStart+structEnd]

	consoleStart := strings.Index(chains, "Console: slices.Concat(edge, []web.Middleware{")
	require.Positive(t, consoleStart, "console chain not found — this guard needs updating")
	consoleEnd := strings.Index(chains[consoleStart:], "\n\t\t}),")
	require.Positive(t, consoleEnd, "console chain end not found — this guard needs updating")
	console := chains[consoleStart : consoleStart+consoleEnd]

	csp := strings.Index(console, "webmw.CSP(")
	session := strings.Index(console, "webmw.Session(")
	tenant := strings.Index(console, "webmw.Tenant")
	require.Positive(t, csp, "CSP middleware missing from the console chain — nonce={ d.Nonce } would silently stop working")
	require.Positive(t, session, "session middleware missing from the console chain")
	require.Positive(t, tenant, "tenant middleware missing from the console chain — handlers would see an unscoped r.Context()")
	require.Less(t, session, tenant, "tenant must run after session or the principal is not on the context yet")

	other := chains[:consoleStart] + chains[consoleStart+consoleEnd:]
	require.NotContains(t, other, "webmw.Session(", "session middleware must not run on the Control, Data, Ingest or Probes chains — those surfaces stay edge-only")
}

func TestWebHandler_InstallsFlashLastOnTheConsoleChain(t *testing.T) {
	src, err := os.ReadFile("http.go")
	require.NoError(t, err)
	source := string(src)
	start := strings.Index(source, "func surfaceChains(")
	require.Positive(t, start)
	end := strings.Index(source[start:], "\n\t}\n}")
	require.Positive(t, end)
	chains := source[start : start+end]
	consoleStart := strings.Index(chains, "Console: slices.Concat(edge, []web.Middleware{")
	require.Positive(t, consoleStart)
	consoleEnd := strings.Index(chains[consoleStart:], "\n\t\t}),")
	require.Positive(t, consoleEnd)
	console := chains[consoleStart : consoleStart+consoleEnd]

	session := strings.Index(console, "webmw.Session(")
	onboarding := strings.Index(console, "webhandlers.OnboardingGate(")
	welcome := strings.Index(console, "webhandlers.WelcomeGate(")
	flash := strings.Index(console, "webmw.Flash(")
	require.Positive(t, flash, "flash middleware missing from the console chain")
	require.Less(t, session, flash, "flash must run after session")
	require.Less(t, onboarding, flash, "a gate redirect before flash would consume the flash")
	require.Less(t, welcome, flash, "a gate redirect before flash would consume the flash")

	other := chains[:consoleStart] + chains[consoleStart+consoleEnd:]
	require.NotContains(t, other, "webmw.Flash(", "flash belongs to the console surface only")
}
