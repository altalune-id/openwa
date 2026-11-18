package templates

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOverviewPage_StepCountAndProgressAgree(t *testing.T) {
	html := render(t, OverviewPage(projectShell(), OverviewView{ProjectSlug: "alpha", CanManage: true, Numbers: []HomeNumber{{ID: "dev_x", Name: "Sales", Status: SessionStatus{Key: "devices.state.connected", Tone: ToneSuccess, Icon: "circle-check"}}}, Steps: HomeSteps{Linked: true, Agent: true}}))
	require.Contains(t, html, `value="2" max="4"`)
	require.Contains(t, html, `data-content="✓"`)
	require.Contains(t, html, `data-content="2"`)
}
