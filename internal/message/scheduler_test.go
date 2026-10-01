package message_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/scheduler"
)

func TestScheduler_RetentionJobShape(t *testing.T) {
	e := newEnv(t)
	jobs := message.NewScheduler(e.svc, discard(), nil).SchedulerJobs()
	require.Len(t, jobs, 1)
	j := jobs[0]
	require.Equal(t, message.RetentionJobName, j.Name)
	require.Equal(t, scheduler.ScopeTenant, j.Scope)
	require.True(t, j.Singleton)
	require.Equal(t, 20*time.Minute, j.Timeout)
	from := time.Date(2026, 9, 28, 2, 59, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC), j.Schedule.Next(from))
	require.Equal(t, time.Date(2026, 9, 29, 3, 0, 0, 0, time.UTC), j.Schedule.Next(from.Add(time.Minute)))
}

func TestScheduler_RunDelegatesToTheService(t *testing.T) {
	e := newEnv(t)
	ref := message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
	e.store.Seed(message.NewInbound(ref, uuid.New(), fakes.MessagePublicID(), message.InboundInput{WAID: "OLD", Type: "text", Timestamp: e.now.AddDate(0, 0, -31)}))
	job := message.NewScheduler(e.svc, discard(), nil).SchedulerJobs()[0]
	require.NoError(t, job.Run(tenant.Into(t.Context(), tenant.Context{OrgID: e.tc.OrgID})))
	require.Empty(t, e.store.All())
}
