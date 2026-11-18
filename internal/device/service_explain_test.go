package device_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/tenant"
)

func TestService_ExplainRulesNormalizesADraftPhone(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)
	draft := device.Rules{Items: []device.Rule{rule("a", cond(device.FieldChatKind, device.OpIs, "dm"), cond(device.FieldSender, device.OpIs, "phone:0811-1122-2333"))}}

	got, err := f.svc.ExplainRules(f.ctx(t), d.ID, draft, device.MatchInput{SenderPhone: "6281111222333"})
	require.NoError(t, err)
	require.Equal(t, device.OutcomeMatched, got.Result.Outcome, "a local-format phone in a draft matches the way it will once saved")
}

func TestService_ExplainRulesMatchesAHiddenPhoneMemberByLID(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)
	group := cond(device.FieldChatKind, device.OpIs, "group")
	in := device.MatchInput{IsGroup: true, ChatJID: "1@g.us", SenderLID: "12345678901234@lid"}

	byLID := device.Rules{Items: []device.Rule{rule("a", group, cond(device.FieldSender, device.OpIs, "12345678901234@lid"))}}
	got, err := f.svc.ExplainRules(f.ctx(t), d.ID, byLID, in)
	require.NoError(t, err)
	require.Equal(t, device.OutcomeMatched, got.Result.Outcome)

	byDigits := device.Rules{Items: []device.Rule{rule("a", group, cond(device.FieldSender, device.OpIs, "phone:12345678901234"))}}
	got, err = f.svc.ExplainRules(f.ctx(t), d.ID, byDigits, in)
	require.NoError(t, err)
	require.Equal(t, device.OutcomeNone, got.Result.Outcome, "a phone equal to a LID's digits never matches")
	require.Equal(t, 1, got.Checks[0].Failed)
}

func TestService_ExplainRulesRefusesAnInvalidDraft(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)
	bad := device.Rules{Items: []device.Rule{rule("a", cond(device.FieldChat, device.OpIs, "not-a-group"))}}
	_, err = f.svc.ExplainRules(f.ctx(t), d.ID, bad, device.MatchInput{})
	require.True(t, device.IsInvalidRulesError(err), "got %v", err)
}

// SECURITY: a sibling project's device is not-found, so its rules cannot be probed through the test box.
func TestService_ExplainRulesRefusesASiblingProjectsDevice(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)
	sibling := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, ProjectID: uuid.New()})
	_, err = f.svc.ExplainRules(sibling, d.ID, device.Rules{Items: []device.Rule{}}, device.MatchInput{})
	require.True(t, device.IsNotFoundError(err), "got %v", err)
}

func TestService_NormalizePhoneUsesTheDefaultCountryCode(t *testing.T) {
	f := newSvcFixture(t)
	got, ok := f.svc.NormalizePhone("0812 3456 7890")
	require.True(t, ok)
	require.Equal(t, "6281234567890", got)
	_, ok = f.svc.NormalizePhone("Rina")
	require.False(t, ok)
}
