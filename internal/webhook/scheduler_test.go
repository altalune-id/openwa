package webhook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/webhook"
	"altalune.id/openwa/scheduler"
)

func TestScheduler_HintBackfillRunsPerOrgUnderALock(t *testing.T) {
	jobs := webhook.NewScheduler(newHarness(t).svc).SchedulerJobs()
	require.Len(t, jobs, 1)
	assert.Equal(t, webhook.HintBackfillJobName, jobs[0].Name)
	assert.Equal(t, scheduler.ScopeTenant, jobs[0].Scope)
	assert.True(t, jobs[0].Singleton)
}
