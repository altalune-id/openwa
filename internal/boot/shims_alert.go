package boot

import (
	"context"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/alert"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/whatsapp"
)

type alertSessions struct{ outages *whatsapp.Outages }

var _ alert.Sessions = alertSessions{}

func (s alertSessions) ProjectsNeedingAlert(ctx context.Context, grace, cooldown time.Duration) ([]uuid.UUID, error) {
	return s.outages.ProjectsNeedingAlert(ctx, grace, cooldown)
}

func (s alertSessions) ClaimOutages(ctx context.Context, grace, cooldown time.Duration) ([]alert.Outage, error) {
	claimed, err := s.outages.Claim(ctx, grace, cooldown)
	if err != nil {
		return nil, err
	}
	out := make([]alert.Outage, len(claimed))
	for i, o := range claimed {
		out[i] = alert.Outage{DeviceID: o.PublicID, Name: o.Name, Phone: o.Phone, DisconnectedAt: o.DisconnectedAt}
	}
	return out, nil
}

type alertMembers struct{ svc *org.Service }

var _ alert.Members = alertMembers{}

func (m alertMembers) Members(ctx context.Context, orgID uuid.UUID) ([]alert.Member, error) {
	profiles, err := m.svc.ListMemberProfiles(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]alert.Member, 0, len(profiles))
	for _, p := range profiles {
		if p.System {
			continue
		}
		out = append(out, alert.Member{Email: p.Email, Locale: p.Locale, Manager: p.Role.CanManage()})
	}
	return out, nil
}

type alertProjects struct {
	projects *project.Service
	orgs     *org.Service
}

var _ alert.Projects = alertProjects{}

func (a alertProjects) Describe(ctx context.Context, projectID uuid.UUID) (alert.Project, error) {
	p, err := a.projects.ByID(ctx, projectID)
	if err != nil {
		return alert.Project{}, err
	}
	o, err := a.orgs.ByID(ctx, p.OrgID)
	if err != nil {
		return alert.Project{}, err
	}
	return alert.Project{Name: p.Name, Slug: p.Slug, OrgSlug: o.Slug}, nil
}
