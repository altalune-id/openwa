package device_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/publicid"
)

func testPublicID(t *testing.T) string {
	t.Helper()
	id, err := publicid.New(device.PublicIDPrefix)
	require.NoError(t, err)
	return id
}

func TestNew_Invariants(t *testing.T) {
	t.Parallel()
	org, proj := uuid.New(), uuid.New()
	cases := []struct {
		name    string
		in      string
		want    string
		invalid bool
	}{
		{"trims", "  Sales  ", "Sales", false},
		{"empty", "   ", "", true},
		{"max 64 runes", strings.Repeat("é", 64), strings.Repeat("é", 64), false},
		{"65 runes", strings.Repeat("é", 65), "", true},
		{"shaped like a public id", "dev_V1StGXR8Z5jdHi6B", "", true},
		{"a dev_ name of another length", "dev_sales", "dev_sales", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pub := testPublicID(t)
			d, err := device.New(org, proj, pub, tc.in)
			if tc.invalid {
				require.True(t, device.IsInvalidNameError(err), "got %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, d.Name)
			require.Equal(t, 1, d.Version)
			require.Equal(t, device.DefaultRules(), d.Rules)
			require.Equal(t, device.GroupMention, d.Rules.GroupMode)
			require.True(t, d.Rules.IgnoreFromMe)
			require.NotEqual(t, uuid.Nil, d.ID)
			require.Equal(t, pub, d.PublicID)
			require.Equal(t, org, d.OrgID)
			require.Equal(t, proj, d.ProjectID)
		})
	}
}

func TestRename(t *testing.T) {
	t.Parallel()
	d, err := device.New(uuid.New(), uuid.New(), testPublicID(t), "one")
	require.NoError(t, err)
	before := d.UpdatedAt
	require.True(t, device.IsInvalidNameError(d.Rename(" ")))
	require.Equal(t, "one", d.Name)
	require.NoError(t, d.Rename(" two "))
	require.Equal(t, "two", d.Name)
	require.False(t, d.UpdatedAt.Before(before))
}

func TestSetRules_ValidatesEveryField(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		rules device.Rules
		field string
	}{
		{"bad group mode", device.Rules{GroupMode: "loud"}, "group_mode"},
		{"sender with plus", device.Rules{GroupMode: device.GroupOpen, AllowedSenders: []string{"+62812"}}, "allowed_senders"},
		{"sender too short", device.Rules{GroupMode: device.GroupOpen, AllowedSenders: []string{"1234567"}}, "allowed_senders"},
		{"group not g.us", device.Rules{GroupMode: device.GroupOpen, AllowedGroups: []string{"628@s.whatsapp.net"}}, "allowed_groups"},
		{"prefix too long", device.Rules{GroupMode: device.GroupOpen, TriggerPrefix: strings.Repeat("!", 17)}, "trigger_prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := device.New(uuid.New(), uuid.New(), testPublicID(t), "x")
			require.NoError(t, err)
			err = d.SetRules(tc.rules)
			require.True(t, device.IsInvalidRulesError(err), "got %v", err)
			var ir *device.InvalidRulesError
			require.ErrorAs(t, err, &ir)
			require.Equal(t, tc.field, ir.Field)
		})
	}
}

func TestSetRules_NormalizesLists(t *testing.T) {
	t.Parallel()
	d, err := device.New(uuid.New(), uuid.New(), testPublicID(t), "x")
	require.NoError(t, err)
	require.NoError(t, d.SetRules(device.Rules{
		GroupMode:      device.GroupOpen,
		AllowedSenders: []string{" 628123456789 ", "", "628123456789", "6281111111111"},
		AllowedGroups:  []string{"120363@g.us", "120363@g.us"},
		TriggerPrefix:  "  !bot ",
		IgnoreFromMe:   false,
	}))
	require.Equal(t, []string{"628123456789", "6281111111111"}, d.Rules.AllowedSenders)
	require.Equal(t, []string{"120363@g.us"}, d.Rules.AllowedGroups)
	require.Equal(t, "!bot", d.Rules.TriggerPrefix)
	require.False(t, d.Rules.IgnoreFromMe)
}

func TestNew_PublicIDShapeCheckTracksPublicIDLength(t *testing.T) {
	t.Parallel()
	shaped := device.PublicIDPrefix + "_" + strings.Repeat("a", publicid.Length)
	_, err := device.New(uuid.New(), uuid.New(), testPublicID(t), shaped)
	require.True(t, device.IsInvalidNameError(err), "got %v", err)
	_, err = device.New(uuid.New(), uuid.New(), testPublicID(t), shaped[:len(shaped)-1])
	require.NoError(t, err)
	_, err = device.New(uuid.New(), uuid.New(), testPublicID(t), shaped+"a")
	require.NoError(t, err)
}
