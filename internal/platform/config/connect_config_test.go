package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidate_RecommendedAgentIsAllOrNoneAndHTTPS(t *testing.T) {
	t.Parallel()
	full := RecommendedAgentConfig{Name: "Agent", ConsoleURL: "https://agent.example.com", GuideURL: "https://example.com/guide"}
	cases := []struct {
		name string
		mut  func(*RecommendedAgentConfig)
		bad  bool
	}{
		{"none", func(a *RecommendedAgentConfig) { *a = RecommendedAgentConfig{} }, false},
		{"all", func(*RecommendedAgentConfig) {}, false},
		{"name only", func(a *RecommendedAgentConfig) { a.ConsoleURL, a.GuideURL = "", "" }, true},
		{"urls only", func(a *RecommendedAgentConfig) { a.Name = "" }, true},
		{"no guide", func(a *RecommendedAgentConfig) { a.GuideURL = "" }, true},
		{"http console", func(a *RecommendedAgentConfig) { a.ConsoleURL = "http://agent.example.com" }, true},
		{"javascript guide", func(a *RecommendedAgentConfig) { a.GuideURL = "javascript:alert(1)" }, true},
		{"long name", func(a *RecommendedAgentConfig) { a.Name = "An agent name that is far longer than forty characters" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := validSelfhosted()
			cfg.Connect.RecommendedAgent = full
			tc.mut(&cfg.Connect.RecommendedAgent)
			err := cfg.Validate()
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestEnvKeys_RecommendedAgentIsOptional(t *testing.T) {
	want := map[string]bool{"connect.recommendedAgent.name": false, "connect.recommendedAgent.consoleURL": false, "connect.recommendedAgent.guideURL": false}
	for _, k := range WalkEnvKeys(EnvPrefix) {
		if _, ok := want[k.YAML]; !ok {
			continue
		}
		want[k.YAML] = true
		require.Empty(t, k.Awareness, k.YAML)
	}
	for key, found := range want {
		require.True(t, found, "%s must be a known env key", key)
	}
	require.Empty(t, Defaults().Connect.RecommendedAgent.Name, "no agent is recommended by default")
}
