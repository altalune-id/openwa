package boot

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/whatsapp"
)

type deviceSessions struct{ svc *whatsapp.Service }

var _ device.Sessions = deviceSessions{}

func (s deviceSessions) StatusByDevices(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]device.SessionStatus, error) {
	in, err := s.svc.StatusByDevices(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]device.SessionStatus, len(in))
	for id, st := range in {
		out[id] = toDeviceStatus(st)
	}
	return out, nil
}

func (s deviceSessions) Link(ctx context.Context, id uuid.UUID) (device.LinkState, error) {
	st, err := s.svc.Link(ctx, id)
	return toDeviceLink(st), err
}

func (s deviceSessions) LinkWithPhone(ctx context.Context, id uuid.UUID, phone string) (device.LinkState, error) {
	st, err := s.svc.LinkWithPhone(ctx, id, phone)
	return toDeviceLink(st), err
}

func (s deviceSessions) LinkState(ctx context.Context, id uuid.UUID) (device.LinkState, error) {
	st, err := s.svc.LinkState(ctx, id)
	return toDeviceLink(st), err
}

func (s deviceSessions) Unlink(ctx context.Context, id uuid.UUID) error { return s.svc.Unlink(ctx, id) }

func (s deviceSessions) Forget(ctx context.Context, id uuid.UUID) error { return s.svc.Forget(ctx, id) }

func toDeviceStatus(st whatsapp.Status) device.SessionStatus {
	return device.SessionStatus{
		State:    device.SessionState(st.State),
		Phone:    st.Phone,
		PushName: st.PushName,
		Reason:   st.Reason,
		LastSeen: st.LastSeen,
		Live:     st.Live,
	}
}

func toDeviceLink(st whatsapp.LinkState) device.LinkState {
	out := device.LinkState{
		ID:          st.ID,
		Method:      device.LinkMethod(st.Method),
		Outcome:     device.LinkOutcome(st.Outcome),
		QR:          st.QR,
		PNG:         st.PNG,
		PairingCode: st.PairingCode,
		ExpiresAt:   st.ExpiresAt,
		StartedAt:   st.StartedAt,
	}
	if st.Err != nil {
		out.Error = st.Err.Error()
	}
	return out
}

type deviceDescriber struct{ store device.Store }

var _ whatsapp.DeviceDescriber = deviceDescriber{}

func (n deviceDescriber) Describe(ctx context.Context, id uuid.UUID) (publicID, name string, err error) {
	d, err := n.store.ByID(ctx, id)
	if err != nil {
		return "", "", err
	}
	return d.PublicID, d.Name, nil
}

type deviceServiceForDataplane struct{ svc *device.Service }

var _ dataplane.Devices = deviceServiceForDataplane{}

func (s deviceServiceForDataplane) List(ctx context.Context) ([]dataplane.DeviceRef, error) {
	views, err := s.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dataplane.DeviceRef, 0, len(views))
	for _, v := range views {
		out = append(out, deviceRefOf(v))
	}
	return out, nil
}

func (s deviceServiceForDataplane) Resolve(ctx context.Context, publicID string) (dataplane.DeviceRef, error) {
	d, err := s.svc.Resolve(ctx, publicID)
	if err != nil {
		return dataplane.DeviceRef{}, err
	}
	return s.Get(ctx, d.ID)
}

func (s deviceServiceForDataplane) Get(ctx context.Context, id uuid.UUID) (dataplane.DeviceRef, error) {
	v, err := s.svc.Get(ctx, id)
	if err != nil {
		return dataplane.DeviceRef{}, err
	}
	return deviceRefOf(v), nil
}

func (s deviceServiceForDataplane) Create(ctx context.Context, name string) (dataplane.DeviceRef, error) {
	d, err := s.svc.Create(ctx, name)
	if err != nil {
		return dataplane.DeviceRef{}, err
	}
	return deviceRefOf(device.DeviceView{Device: d, Status: device.SessionStatus{State: device.SessionUnlinked}}), nil
}

func (s deviceServiceForDataplane) Update(ctx context.Context, id uuid.UUID, name *string, rules *dataplane.RulesRef, ifVersion int) (dataplane.DeviceRef, error) {
	var u device.Update
	u.Name = name
	switch {
	case rules == nil:
	case rules.Items != nil:
		r := rulesFromRef(*rules)
		u.Rules = &r
	default:
		l := device.Legacy{
			GroupMode:      device.GroupMode(rules.GroupMode),
			AllowedSenders: rules.AllowedSenders,
			AllowedGroups:  rules.AllowedGroups,
			TriggerPrefix:  rules.TriggerPrefix,
			IgnoreFromMe:   rules.IgnoreFromMe,
		}
		u.Legacy = &l
	}
	if _, err := s.svc.Update(ctx, id, u, ifVersion); err != nil {
		return dataplane.DeviceRef{}, err
	}
	return s.Get(ctx, id)
}

func (s deviceServiceForDataplane) Delete(ctx context.Context, id uuid.UUID) error {
	return s.svc.Delete(ctx, id)
}

func (s deviceServiceForDataplane) StartLink(ctx context.Context, id uuid.UUID) (dataplane.LinkRef, error) {
	st, err := s.svc.StartLink(ctx, id)
	return linkRefOf(st), err
}

func (s deviceServiceForDataplane) LinkWithPhone(ctx context.Context, id uuid.UUID, phone string) (dataplane.LinkRef, error) {
	st, err := s.svc.LinkWithPhone(ctx, id, phone)
	return linkRefOf(st), err
}

func (s deviceServiceForDataplane) LinkState(ctx context.Context, id uuid.UUID) (dataplane.LinkRef, error) {
	st, err := s.svc.LinkState(ctx, id)
	return linkRefOf(st), err
}

func (s deviceServiceForDataplane) Unlink(ctx context.Context, id uuid.UUID) error {
	return s.svc.Unlink(ctx, id)
}

func deviceRefOf(v device.DeviceView) dataplane.DeviceRef {
	d := v.Device
	state := string(v.Status.State)
	if state == "" {
		state = string(device.SessionUnlinked)
	}
	return dataplane.DeviceRef{
		ID:         d.ID,
		PublicID:   d.PublicID,
		Name:       d.Name,
		Rules:      rulesRefOf(d.Rules),
		Version:    d.Version,
		State:      state,
		Phone:      v.Status.Phone,
		PushName:   v.Status.PushName,
		LastSeenAt: v.Status.LastSeen,
	}
}

func rulesRefOf(r device.Rules) dataplane.RulesRef {
	l := r.LegacyView()
	out := dataplane.RulesRef{
		GroupMode: string(l.GroupMode), AllowedSenders: l.AllowedSenders, AllowedGroups: l.AllowedGroups, TriggerPrefix: l.TriggerPrefix,
		IgnoreFromMe: r.IgnoreFromMe, Items: make([]dataplane.RuleRef, 0, len(r.Items)),
	}
	for _, it := range r.Items {
		rr := dataplane.RuleRef{ID: it.ID, Name: it.Name, Enabled: it.Enabled, Action: string(it.Action), Conditions: make([]dataplane.ConditionRef, 0, len(it.Conditions))}
		for _, c := range it.Conditions {
			rr.Conditions = append(rr.Conditions, dataplane.ConditionRef{Field: string(c.Field), Op: string(c.Op), Values: c.Values})
		}
		out.Items = append(out.Items, rr)
	}
	return out
}

func rulesFromRef(r dataplane.RulesRef) device.Rules {
	out := device.Rules{IgnoreFromMe: r.IgnoreFromMe, Items: make([]device.Rule, 0, len(r.Items))}
	for _, it := range r.Items {
		rule := device.Rule{ID: it.ID, Name: it.Name, Enabled: it.Enabled, Action: device.Action(it.Action), Conditions: make([]device.Condition, 0, len(it.Conditions))}
		for _, c := range it.Conditions {
			rule.Conditions = append(rule.Conditions, device.Condition{Field: device.Field(c.Field), Op: device.Op(c.Op), Values: c.Values})
		}
		out.Items = append(out.Items, rule)
	}
	return out
}

func linkRefOf(st device.LinkState) dataplane.LinkRef {
	return dataplane.LinkRef{
		ID:          st.ID,
		Method:      string(st.Method),
		Outcome:     string(st.Outcome),
		QR:          st.QR,
		PNG:         st.PNG,
		PairingCode: st.PairingCode,
		ExpiresAt:   st.ExpiresAt,
		StartedAt:   st.StartedAt,
	}
}
