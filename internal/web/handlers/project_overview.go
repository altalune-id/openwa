package handlers

import (
	"net/http"
	"slices"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
	"altalune.id/openwa/internal/webhook"
)

// ProjectOverviewHandler serves the project Home page.
type ProjectOverviewHandler struct {
	Deps
	Devices  *device.Service
	Messages *message.Service
	Webhooks *webhook.Service
}

// NewProjectOverviewHandler wires the Home page; devices, messages and webhooks may be nil.
func NewProjectOverviewHandler(d Deps, projects *project.Service, devices *device.Service, messages *message.Service, webhooks *webhook.Service) *ProjectOverviewHandler {
	d.Projects = projects
	return &ProjectOverviewHandler{Deps: d, Devices: devices, Messages: messages, Webhooks: webhooks}
}

// Register mounts the overview route.
func (h *ProjectOverviewHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/overview", h.GetOverview)
}

// GetOverview renders Home: the numbers, their activity and the setup checklist.
func (h *ProjectOverviewHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	h.rememberProject(sc)
	l := h.LayoutForProject(sc.req, pageTitle(sc.req, "nav.home"), sc.org.Slug, sc.project, "overview")
	v, err := h.home(sc)
	v.ProjectSlug, v.CanManage, v.ChecklistOpen = sc.project.Slug, h.canManageProject(sc), r.URL.Query().Get("checklist") == "open"
	if err != nil {
		h.LogErr("web overview: load", err)
		v.LoadFailed = true
		RenderStatus(w, sc.req, http.StatusInternalServerError, templates.OverviewLayout(l, v))
		return
	}
	Render(w, sc.req, templates.OverviewLayout(l, v))
}

func (h *ProjectOverviewHandler) home(sc ProjectScope) (templates.OverviewView, error) {
	ctx := sc.req.Context()
	var v templates.OverviewView
	var views []device.DeviceView
	if h.Devices != nil {
		var err error
		if views, err = h.Devices.List(ctx); err != nil {
			return v, err
		}
	}
	activity := map[uuid.UUID]message.DeviceActivity{}
	if h.Messages != nil && len(views) > 0 {
		ids := make([]uuid.UUID, 0, len(views))
		for _, dv := range views {
			ids = append(ids, dv.Device.ID)
		}
		var err error
		if activity, err = h.Messages.Activity(ctx, ids); err != nil {
			return v, err
		}
	}
	for _, dv := range views {
		a := activity[dv.Device.ID]
		v.Numbers = append(v.Numbers, templates.HomeNumber{
			ID: dv.Device.PublicID, Name: dv.Device.Name, Phone: dv.Status.Phone,
			Status: sessionStatus(dv.Status.State), NeedsFix: dv.Status.State == device.SessionLoggedOut, LastAt: a.LastAt, Last24h: a.Last24h,
		})
	}
	v.Steps, v.RulesDevice = checklistSteps(views)
	if h.Webhooks != nil {
		hooks, err := h.Webhooks.List(ctx)
		if err != nil {
			return v, err
		}
		v.Steps.Agent = slices.ContainsFunc(hooks, func(e *webhook.Endpoint) bool {
			return e.Active && e.Subscribes(events.MessageMatched)
		})
	}
	if h.Messages != nil {
		var err error
		if v.Steps.Tested, err = h.Messages.HasMatched(ctx); err != nil {
			return v, err
		}
	}
	return v, nil
}

func checklistSteps(views []device.DeviceView) (s templates.HomeSteps, rulesDevice string) {
	for _, dv := range views {
		ever := dv.Status.Phone != "" || dv.Status.LastConnectedAt != nil
		if ever && !s.Linked {
			rulesDevice = dv.Device.PublicID
		}
		s.Linked = s.Linked || ever
		if !dv.Device.Rules.IsDefault() && slices.ContainsFunc(dv.Device.Rules.Items, func(r device.Rule) bool {
			return r.Enabled && r.Action == device.ActionMatch
		}) {
			s.Rules = true
		}
	}
	if rulesDevice == "" && len(views) > 0 {
		rulesDevice = views[0].Device.PublicID
	}
	return s, rulesDevice
}
