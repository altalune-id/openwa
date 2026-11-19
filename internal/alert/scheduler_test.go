package alert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/scheduler"
)

func TestScheduler_DisconnectJobRunsEveryMinutePerOrgOnOneReplica(t *testing.T) {
	jobs := NewScheduler(&Service{}).SchedulerJobs()
	require.Len(t, jobs, 1)
	j := jobs[0]
	assert.Equal(t, DisconnectJobName, j.Name)
	assert.Equal(t, scheduler.ScopeTenant, j.Scope)
	assert.True(t, j.Singleton)
	assert.Equal(t, 30*time.Second, j.Timeout)
	base := time.Date(2026, 11, 18, 10, 0, 30, 0, time.UTC)
	assert.Equal(t, time.Minute, j.Schedule.Next(base.Add(time.Minute)).Sub(j.Schedule.Next(base)))
}
