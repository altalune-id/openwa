package whatsapp

import (
	"cmp"
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"altalune.id/openwa/internal/platform/publicid"
)

const (
	claimBatch         = 100
	closeBudget        = 10 * time.Second
	openBudget         = 20 * time.Second
	shutdownBudget     = 5 * time.Second
	releaseBudget      = 5 * time.Second
	closeParallel      = 8
	firstCodeWait      = 15 * time.Second
	defaultLeaseTTL    = 45 * time.Second
	defaultLeaseEvery  = 15 * time.Second
	defaultLinkTimeout = 3 * time.Minute
)

// RuntimeConfig tunes the lease loop and link attempts; Owner identifies this process in lease rows.
type RuntimeConfig struct {
	Owner         string
	LeaseTTL      time.Duration
	LeaseInterval time.Duration
	LinkTimeout   time.Duration
	ParkFor       time.Duration
}

type held struct {
	ref    SessionRef
	sess   EngineSession
	cancel context.CancelFunc
}

type doomedDevice struct {
	ref    SessionRef
	purged bool
}

type attempt struct {
	ref       SessionRef
	session   EngineSession
	state     LinkState
	promoted  bool
	stopped   bool
	pairing   chan struct{}
	cancel    context.CancelFunc
	first     chan struct{}
	firstOnce sync.Once
}

func (a *attempt) signal() { a.firstOnce.Do(func() { close(a.first) }) }

// Runtime owns the engine sessions of the leases this process holds, and every link attempt.
type Runtime struct {
	engine    Engine
	leases    LeaseStore
	cfg       RuntimeConfig
	log       *slog.Logger
	sink      EventSink
	openSlots chan struct{}

	mu        sync.Mutex
	base      context.Context
	closing   bool
	fenced    bool
	verified  time.Time
	held      map[uuid.UUID]*held
	unlinking map[uuid.UUID]*held
	linking   map[uuid.UUID]*attempt
	owned     map[uuid.UUID]Lease
	parked    map[uuid.UUID]time.Time
	openErr   map[uuid.UUID]string
	doomed    map[uuid.UUID]doomedDevice
	settling  bool
	wg        sync.WaitGroup
}

var _ SessionRuntime = (*Runtime)(nil)

// NewRuntime builds a runtime; a zero duration takes its default (TTL 45s, interval 15s, link 3m, park 5 x TTL).
func NewRuntime(engine Engine, leases LeaseStore, cfg RuntimeConfig, log *slog.Logger) *Runtime {
	cfg.LeaseTTL = cmp.Or(cfg.LeaseTTL, defaultLeaseTTL)
	cfg.LeaseInterval = cmp.Or(cfg.LeaseInterval, defaultLeaseEvery)
	cfg.LinkTimeout = cmp.Or(cfg.LinkTimeout, defaultLinkTimeout)
	cfg.ParkFor = cmp.Or(cfg.ParkFor, 5*cfg.LeaseTTL)
	return &Runtime{
		engine:    engine,
		leases:    leases,
		cfg:       cfg,
		log:       log.With("worker", "whatsapp.runtime"),
		openSlots: make(chan struct{}, closeParallel),
		held:      map[uuid.UUID]*held{},
		unlinking: map[uuid.UUID]*held{},
		linking:   map[uuid.UUID]*attempt{},
		owned:     map[uuid.UUID]Lease{},
		parked:    map[uuid.UUID]time.Time{},
		openErr:   map[uuid.UUID]string{},
		doomed:    map[uuid.UUID]doomedDevice{},
	}
}

// Subscribe sets the one sink engine events are forwarded to; it is called once, before Run.
func (r *Runtime) Subscribe(s EventSink) { r.sink = s }

// Name identifies the worker to the supervisor.
func (r *Runtime) Name() string { return "whatsapp.runtime" }

// Run claims and renews leases every LeaseInterval until ctx is done, then closes every session and releases every lease; it returns nil on cancel.
func (r *Runtime) Run(ctx context.Context) error {
	r.mu.Lock()
	r.base = ctx
	r.verified = time.Now()
	r.mu.Unlock()

	r.tick(ctx)
	t := time.NewTicker(r.cfg.LeaseInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.shutdown(ctx)
			return nil
		case <-t.C:
			r.tick(ctx)
		}
	}
}

// Link starts a QR link attempt and waits briefly for its first code.
func (r *Runtime) Link(ctx context.Context, ref SessionRef) (LinkState, error) {
	a, err := r.startAttempt(ctx, ref, LinkMethodQR)
	if err != nil {
		return LinkState{}, err
	}
	r.awaitFirst(ctx, a)
	return r.LinkState(ref.DeviceID), nil
}

// LinkWithPhone reuses the pending attempt or starts one, then asks the engine for a pairing code once per attempt.
func (r *Runtime) LinkWithPhone(ctx context.Context, ref SessionRef, phone string) (LinkState, error) {
	digits, err := NormalizePhone(phone)
	if err != nil {
		return LinkState{}, err
	}
	a, err := r.phoneAttempt(ctx, ref)
	if err != nil {
		return LinkState{}, err
	}
	r.awaitFirst(ctx, a)
	for {
		r.mu.Lock()
		if a.state.PairingCode != "" || a.state.Outcome != OutcomePending || a.session == nil {
			st := a.state
			r.mu.Unlock()
			return st, nil
		}
		wait := a.pairing
		if wait == nil {
			a.pairing = make(chan struct{})
			sess := a.session
			r.mu.Unlock()
			return r.requestCode(ctx, a, sess, digits)
		}
		r.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return LinkState{}, ctx.Err()
		}
	}
}

func (r *Runtime) phoneAttempt(ctx context.Context, ref SessionRef) (*attempt, error) {
	r.mu.Lock()
	a := r.linking[ref.DeviceID]
	reuse := a != nil && a.state.Outcome == OutcomePending && !a.stopped && !a.promoted
	r.mu.Unlock()
	if reuse {
		return a, nil
	}
	return r.startAttempt(ctx, ref, LinkMethodPhone)
}

func (r *Runtime) requestCode(ctx context.Context, a *attempt, sess EngineSession, digits string) (LinkState, error) {
	code, err := sess.LinkWithPhone(ctx, digits)
	r.mu.Lock()
	defer r.mu.Unlock()
	close(a.pairing)
	a.pairing = nil
	if err != nil {
		return LinkState{}, engineErr("pair phone", err)
	}
	a.state.Method = LinkMethodPhone
	a.state.PairingCode = code
	return a.state, nil
}

// LinkState returns deviceID's attempt, or Outcome none when this process has none.
func (r *Runtime) LinkState(deviceID uuid.UUID) LinkState {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.linking[deviceID]
	if !ok {
		return LinkState{Outcome: OutcomeNone}
	}
	return a.state
}

// Unlink logs a held device out, purges it and deletes its lease; a pending attempt is cancelled instead.
func (r *Runtime) Unlink(ctx context.Context, deviceID uuid.UUID) error {
	r.mu.Lock()
	h := r.held[deviceID]
	a := r.linking[deviceID]
	pending := a != nil && a.state.Outcome == OutcomePending && !a.promoted
	if h == nil || h.sess == nil {
		r.mu.Unlock()
		if pending {
			r.stop(a)
			return nil
		}
		return &NotOwnedError{ID: deviceID.String()}
	}
	delete(r.held, deviceID)
	r.unlinking[deviceID] = h
	r.mu.Unlock()
	defer r.doneUnlinking(deviceID)

	if err := h.sess.Unlink(ctx); err != nil && !IsSessionGoneError(err) {
		r.restoreHeld(h)
		return engineErr("unlink", err)
	}
	r.mu.Lock()
	if a != nil && r.linking[deviceID] == a {
		delete(r.linking, deviceID)
	}
	r.forgetLocked(deviceID)
	r.mu.Unlock()
	r.closeSession(h.sess)
	if err := r.retire(ctx, h.ref, false); err != nil {
		return err
	}
	r.emit(h.ref, StateLoggedOut, ReasonUnlinked)
	return nil
}

func (r *Runtime) retire(ctx context.Context, ref SessionRef, purged bool) error {
	id := ref.DeviceID
	if !purged {
		if err := r.engine.Purge(ctx, ref); err != nil {
			r.doom(ref, false)
			return engineErr("purge", err)
		}
	}
	if err := r.leases.Delete(ctx, id); err != nil {
		r.doom(ref, true)
		return &EngineError{Op: "lease delete", Err: err}
	}
	r.mu.Lock()
	delete(r.doomed, id)
	r.mu.Unlock()
	return nil
}

func (r *Runtime) doom(ref SessionRef, purged bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return
	}
	prev, ok := r.doomed[ref.DeviceID]
	r.doomed[ref.DeviceID] = doomedDevice{ref: ref, purged: purged || (ok && prev.purged)}
}

func (r *Runtime) settleDoomed(ctx context.Context) {
	defer func() {
		r.mu.Lock()
		r.settling = false
		r.mu.Unlock()
	}()
	r.mu.Lock()
	batch := slices.Collect(maps.Values(r.doomed))
	r.mu.Unlock()
	for _, d := range batch {
		if ctx.Err() != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, r.cfg.LeaseInterval)
		err := r.retire(cctx, d.ref, d.purged)
		cancel()
		if err != nil {
			r.log.WarnContext(ctx, "whatsapp: retiring a logged-out device failed; retrying next tick", "device_id", d.ref.DeviceID, "err", err)
		}
	}
}

func (r *Runtime) doneUnlinking(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.unlinking, id)
}

func (r *Runtime) restoreHeld(h *held) {
	id := h.ref.DeviceID
	r.mu.Lock()
	_, taken := r.held[id]
	keep := !r.closing && !r.fenced && !taken
	if keep {
		r.held[id] = h
	}
	r.mu.Unlock()
	if !keep {
		r.closeSession(h.sess)
	}
}

// Forget logs out best effort, then purges engine state and deletes the lease.
func (r *Runtime) Forget(ctx context.Context, ref SessionRef) error {
	id := ref.DeviceID
	r.mu.Lock()
	h := r.held[id]
	a := r.linking[id]
	delete(r.held, id)
	delete(r.linking, id)
	r.forgetLocked(id)
	r.unlinking[id] = &held{ref: ref}
	r.mu.Unlock()
	defer r.doneUnlinking(id)
	if a != nil {
		r.stop(a)
	}
	if h != nil && h.sess != nil {
		if err := h.sess.Unlink(ctx); err != nil {
			r.log.DebugContext(ctx, "whatsapp: forget: logout failed, purging anyway", "device_id", id, "err", err)
		}
		r.closeSession(h.sess)
		if ref.JID == "" {
			ref.JID = h.ref.JID
		}
	}
	return r.retire(ctx, ref, false)
}

// Live reports whether this process holds deviceID's lease and its engine session is connected.
func (r *Runtime) Live(deviceID uuid.UUID) bool {
	s, ok := r.Session(deviceID)
	return ok && s.Connected()
}

// Session returns the open engine session for a held device.
func (r *Runtime) Session(deviceID uuid.UUID) (EngineSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.held[deviceID]
	if !ok || h.sess == nil {
		return nil, false
	}
	return h.sess, true
}

func (r *Runtime) tick(ctx context.Context) {
	now := time.Now()
	r.fence(ctx, now)
	rctx, rcancel := context.WithTimeout(ctx, r.cfg.LeaseInterval)
	renewed, err := r.leases.Renew(rctx, r.cfg.Owner, r.cfg.LeaseTTL)
	rcancel()
	if err != nil {
		r.log.WarnContext(ctx, "whatsapp: lease renew failed", "err", err)
		r.fence(ctx, time.Now())
		return
	}
	keep := make(map[uuid.UUID]bool, len(renewed))
	for _, id := range renewed {
		keep[id] = true
	}

	r.mu.Lock()
	r.verified, r.fenced = now, false
	var lost []*held
	for id, h := range r.held {
		if keep[id] {
			continue
		}
		if h.sess == nil {
			r.dropHeldLocked(id)
			continue
		}
		lost = append(lost, h)
	}
	if len(lost) > 0 && r.spawnLocked(func() { r.closeAll(context.Background(), lost, ReasonLeaseLost) }) {
		for _, h := range lost {
			r.dropHeldLocked(h.ref.DeviceID)
		}
	}
	for id := range r.owned {
		if !keep[id] {
			r.forgetLocked(id)
		}
	}
	retry := r.retryableLocked(now)
	if len(r.doomed) > 0 && !r.settling && r.spawnLocked(func() { r.settleDoomed(ctx) }) {
		r.settling = true
	}
	r.mu.Unlock()

	for _, l := range retry {
		r.open(ctx, l)
	}

	cctx, ccancel := context.WithTimeout(ctx, r.cfg.LeaseInterval)
	claimed, err := r.leases.Claim(cctx, r.cfg.Owner, r.cfg.LeaseTTL, claimBatch)
	ccancel()
	if err != nil {
		r.log.WarnContext(ctx, "whatsapp: lease claim failed", "err", err)
		return
	}
	for _, l := range claimed {
		r.open(ctx, l)
	}
}

func (r *Runtime) fenceDueLocked(now time.Time) bool {
	return now.Sub(r.verified)+r.cfg.LeaseInterval+r.cfg.LeaseInterval/3 >= r.cfg.LeaseTTL
}

func (r *Runtime) dropHeldLocked(id uuid.UUID) {
	h := r.held[id]
	if h == nil {
		return
	}
	delete(r.held, id)
	if h.cancel != nil {
		h.cancel()
	}
}

// SECURITY: fences while the next tick could still land before the lease expires (elapsed since the last verified renew + LeaseInterval + slack >= LeaseTTL), so a late or slow tick never lets another replica claim first.
func (r *Runtime) fence(ctx context.Context, now time.Time) {
	r.mu.Lock()
	if r.fenced || !r.fenceDueLocked(now) {
		r.mu.Unlock()
		return
	}
	r.fenced = true
	var live []*held
	for id, h := range r.held {
		r.dropHeldLocked(id)
		r.owned[id] = Lease{DeviceID: id, OrgID: h.ref.OrgID, ProjectID: h.ref.ProjectID, JID: h.ref.JID}
		if h.sess != nil {
			live = append(live, h)
		}
	}
	r.spawnLocked(func() { r.closeAll(context.Background(), live, ReasonLeaseUnverified) })
	r.mu.Unlock()
	r.log.ErrorContext(ctx, "whatsapp: leases unverified; closing every held session", "sessions", len(live))
}

func (r *Runtime) open(ctx context.Context, l Lease) {
	id := l.DeviceID
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busyLocked(id) {
		return
	}
	r.owned[id] = l
	if r.parkedLocked(id, time.Now()) {
		return
	}
	octx, cancel := context.WithCancel(ctx)
	placeholder := &held{ref: SessionRef{DeviceID: id, OrgID: l.OrgID, ProjectID: l.ProjectID, JID: l.JID}, cancel: cancel}
	if !r.spawnLocked(func() { r.openOne(octx, cancel, placeholder) }) {
		cancel()
		return
	}
	r.held[id] = placeholder
}

func (r *Runtime) openOne(ctx context.Context, cancelOpen context.CancelFunc, placeholder *held) {
	defer cancelOpen()
	ref := placeholder.ref
	id := ref.DeviceID
	select {
	case r.openSlots <- struct{}{}:
	case <-ctx.Done():
		r.dropPlaceholder(placeholder)
		return
	}
	defer func() { <-r.openSlots }()

	octx, cancel := context.WithTimeout(ctx, openBudget)
	defer cancel()
	sess, err := r.engine.Open(octx, ref, routedSink{r: r, id: id})
	if err != nil {
		if !r.dropPlaceholder(placeholder) || ctx.Err() != nil {
			return
		}
		if IsSessionGoneError(err) {
			r.gone(ctx, ref)
			return
		}
		reason := ReasonOpenFailedPrefix + err.Error()
		if r.noteOpenFailure(id, reason) {
			r.log.WarnContext(ctx, "whatsapp: open failed; retrying every tick", "device_id", id, "err", err)
			r.emit(ref, StateDisconnected, reason)
		}
		return
	}
	r.mu.Lock()
	current := r.held[id] == placeholder
	if current && r.fenceDueLocked(time.Now()) {
		delete(r.held, id)
		current = false
	}
	if current {
		placeholder.sess = sess
		delete(r.owned, id)
		delete(r.openErr, id)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.closeSession(sess)
}

func (r *Runtime) dropPlaceholder(p *held) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.held[p.ref.DeviceID] != p {
		return false
	}
	delete(r.held, p.ref.DeviceID)
	return true
}

func (r *Runtime) noteOpenFailure(id uuid.UUID, reason string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.openErr[id] == reason {
		return false
	}
	r.openErr[id] = reason
	return true
}

func (r *Runtime) gone(ctx context.Context, ref SessionRef) {
	r.mu.Lock()
	r.forgetLocked(ref.DeviceID)
	r.mu.Unlock()
	if err := r.retire(ctx, ref, false); err != nil {
		r.log.WarnContext(ctx, "whatsapp: retiring a missing device failed", "device_id", ref.DeviceID, "err", err)
	}
	r.emit(ref, StateLoggedOut, ReasonDeviceMissing)
}

func (r *Runtime) startAttempt(ctx context.Context, ref SessionRef, method LinkMethod) (*attempt, error) {
	id := ref.DeviceID
	fresh := SessionRef{DeviceID: id, OrgID: ref.OrgID, ProjectID: ref.ProjectID}
	r.mu.Lock()
	base, closing := r.base, r.closing
	busy := r.busyLocked(id)
	r.mu.Unlock()
	if base == nil || closing {
		return nil, &NotOwnedError{ID: id.String()}
	}
	if busy {
		return nil, &AlreadyLinkedError{ID: id.String()}
	}
	exists, err := r.leases.Exists(ctx, id)
	if err != nil {
		return nil, &EngineError{Op: "lease lookup", Err: err}
	}
	if exists {
		return nil, &AlreadyLinkedError{ID: id.String()}
	}
	linkID, err := publicid.New(LinkIDPrefix)
	if err != nil {
		return nil, &EngineError{Op: "link id", Err: err}
	}

	actx, cancel := context.WithTimeout(base, r.cfg.LinkTimeout)
	a := &attempt{
		ref:    fresh,
		state:  LinkState{ID: linkID, Method: method, Outcome: OutcomePending, StartedAt: time.Now().UTC()},
		cancel: cancel,
		first:  make(chan struct{}),
	}
	r.mu.Lock()
	if r.closing || r.busyLocked(id) {
		shut := r.closing
		r.mu.Unlock()
		cancel()
		if shut {
			return nil, &NotOwnedError{ID: id.String()}
		}
		return nil, &AlreadyLinkedError{ID: id.String()}
	}
	r.linking[id] = a
	r.mu.Unlock()

	octx, ocancel := context.WithTimeout(ctx, openBudget)
	sess, err := r.engine.Open(octx, fresh, routedSink{r: r, id: id})
	ocancel()
	if err != nil {
		r.dropAttempt(a)
		cancel()
		return nil, engineErr("open", err)
	}
	events, err := sess.Link(actx)
	if err != nil {
		r.dropAttempt(a)
		cancel()
		r.closeSession(sess)
		return nil, engineErr("link", err)
	}
	r.mu.Lock()
	a.session = sess
	spawned := r.spawnLocked(func() { r.consume(actx, base, a, events) })
	r.mu.Unlock()
	if !spawned {
		r.dropAttempt(a)
		cancel()
		r.closeSession(sess)
		return nil, &NotOwnedError{ID: id.String()}
	}
	return a, nil
}

func (r *Runtime) consume(ctx, base context.Context, a *attempt, events <-chan LinkEvent) {
	outcome, reason := r.follow(ctx, base, a, events)
	a.signal()
	if outcome != "" {
		r.mu.Lock()
		won := !a.promoted
		if won {
			a.state.Outcome = outcome
		}
		sess := a.session
		r.mu.Unlock()
		if won {
			r.closeSession(sess)
			r.emit(a.ref, StateUnlinked, reason)
		}
	}
	a.cancel()

	t := time.NewTimer(r.cfg.LinkTimeout)
	defer t.Stop()
	select {
	case <-t.C:
	case <-base.Done():
	}
	r.dropAttempt(a)
}

func (r *Runtime) follow(ctx, base context.Context, a *attempt, events <-chan LinkEvent) (outcome LinkOutcome, reason string) {
	for {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			promoted, stopped := a.promoted, a.stopped
			r.mu.Unlock()
			switch {
			case promoted:
				return "", ""
			case stopped, base.Err() != nil:
				return OutcomeFailed, ReasonLinkCancelled
			}
			return OutcomeTimeout, ReasonLinkTimeout
		case ev, ok := <-events:
			if !ok {
				r.mu.Lock()
				promoted := a.promoted
				r.mu.Unlock()
				if promoted {
					return "", ""
				}
				return OutcomeFailed, ReasonLinkFailed
			}
			switch ev.Kind {
			case LinkEventCode:
				r.setCode(ctx, a, ev)
				a.signal()
			case LinkEventSuccess:
				return "", ""
			case LinkEventTimeout:
				return OutcomeTimeout, ReasonQRTimeout
			case LinkEventUnsupported:
				r.setErr(a, ev.Err)
				return OutcomeFailed, ReasonUnsupported
			default:
				r.setErr(a, ev.Err)
				return OutcomeFailed, ReasonLinkFailed
			}
		}
	}
}

func (r *Runtime) setCode(ctx context.Context, a *attempt, ev LinkEvent) {
	png, err := renderQR(ev.Code)
	if err != nil {
		r.log.WarnContext(ctx, "whatsapp: qr render failed; serving the raw payload only", "device_id", a.ref.DeviceID, "err", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a.state.QR = ev.Code
	a.state.PNG = png
	a.state.ExpiresAt = ev.Expires
}

func (r *Runtime) setErr(a *attempt, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a.state.Err = err
}

func (r *Runtime) awaitFirst(ctx context.Context, a *attempt) {
	t := time.NewTimer(firstCodeWait)
	defer t.Stop()
	select {
	case <-a.first:
	case <-ctx.Done():
	case <-t.C:
	}
}

func (r *Runtime) stop(a *attempt) {
	r.mu.Lock()
	a.stopped = true
	r.mu.Unlock()
	a.cancel()
}

func (r *Runtime) dropAttempt(a *attempt) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.linking[a.ref.DeviceID] == a {
		delete(r.linking, a.ref.DeviceID)
	}
}

func (r *Runtime) onLinked(id uuid.UUID, ref SessionRef, ident Identity) {
	r.mu.Lock()
	a := r.linking[id]
	if a == nil {
		r.mu.Unlock()
		r.subscriber().OnLinked(ref, ident)
		return
	}
	linked := a.ref
	linked.JID = ident.JID
	sess := a.session
	if a.state.Outcome != OutcomePending || a.stopped || a.promoted {
		r.spawnLocked(func() { r.discardPairing(sess, linked) })
		r.mu.Unlock()
		return
	}
	if r.spawnLocked(func() { r.promote(a, sess, linked, ident) }) {
		a.promoted = true
	}
	r.mu.Unlock()
}

func (r *Runtime) promote(a *attempt, sess EngineSession, linked SessionRef, ident Identity) {
	id := linked.DeviceID
	ctx, cancel := context.WithTimeout(context.Background(), closeBudget)
	defer cancel()
	exp := time.Now().Add(r.cfg.LeaseTTL)
	lease := Lease{DeviceID: id, OrgID: linked.OrgID, ProjectID: linked.ProjectID, JID: ident.JID, Owner: r.cfg.Owner, ExpiresAt: &exp}
	if err := r.leases.Upsert(ctx, lease); err != nil {
		r.log.ErrorContext(ctx, "whatsapp: lease write after pairing failed", "device_id", id, "err", err)
		r.mu.Lock()
		a.state.Outcome = OutcomeFailed
		a.state.Err = err
		r.mu.Unlock()
		a.cancel()
		r.closeSession(sess)
		if perr := r.engine.Purge(ctx, linked); perr != nil {
			r.log.WarnContext(ctx, "whatsapp: purge after a failed lease write failed", "device_id", id, "err", perr)
		}
		r.emit(a.ref, StateUnlinked, ReasonLeaseWriteFailed)
		return
	}
	r.mu.Lock()
	a.state.Outcome = OutcomeConnected
	hold := !r.closing && !r.fenced
	switch {
	case hold:
		r.held[id] = &held{ref: linked, sess: sess}
	case !r.closing:
		r.owned[id] = Lease{DeviceID: id, OrgID: linked.OrgID, ProjectID: linked.ProjectID, JID: ident.JID}
	}
	r.mu.Unlock()
	a.cancel()
	if !hold {
		r.closeSession(sess)
	}
	r.subscriber().OnLinked(linked, ident)
}

func (r *Runtime) discardPairing(sess EngineSession, linked SessionRef) {
	r.closeSession(sess)
	ctx, cancel := context.WithTimeout(context.Background(), closeBudget)
	defer cancel()
	r.log.InfoContext(ctx, "whatsapp: pairing completed after its attempt ended; discarding it", "device_id", linked.DeviceID)
	if err := r.engine.Purge(ctx, linked); err != nil {
		r.log.WarnContext(ctx, "whatsapp: purge of a late pairing failed", "device_id", linked.DeviceID, "err", err)
	}
}

func (r *Runtime) onEngineState(id uuid.UUID, ref SessionRef, st State, reason string) {
	r.mu.Lock()
	if _, ok := r.unlinking[id]; ok {
		r.mu.Unlock()
		return
	}
	h := r.held[id]
	if st == StateLoggedOut && h != nil {
		if r.spawnLocked(func() { r.loggedOut(h, reason) }) {
			r.dropHeldLocked(id)
			r.forgetLocked(id)
		}
		r.mu.Unlock()
		return
	}
	if h != nil && h.sess != nil {
		if until, park := r.parkUntil(st, reason, time.Now()); park {
			if r.spawnLocked(func() {
				r.closeSession(h.sess)
				r.emit(h.ref, StateDisconnected, reason)
			}) {
				r.dropHeldLocked(id)
				r.owned[id] = Lease{DeviceID: id, OrgID: h.ref.OrgID, ProjectID: h.ref.ProjectID, JID: h.ref.JID}
				r.parked[id] = until
			}
			r.mu.Unlock()
			return
		}
	}
	r.mu.Unlock()
	r.subscriber().OnState(ref, st, reason)
}

func (r *Runtime) loggedOut(h *held, reason string) {
	id := h.ref.DeviceID
	r.closeSession(h.sess)
	ctx, cancel := context.WithTimeout(context.Background(), closeBudget)
	defer cancel()
	if err := r.retire(ctx, h.ref, false); err != nil {
		r.log.WarnContext(ctx, "whatsapp: retiring a logged-out device failed", "device_id", id, "err", err)
	}
	r.emit(h.ref, StateLoggedOut, reason)
}

// NOTE: a zero time parks until the process restarts.
func (r *Runtime) parkUntil(st State, reason string, now time.Time) (time.Time, bool) {
	if st != StateDisconnected {
		return time.Time{}, false
	}
	switch {
	case reason == ReasonClientOutdated:
		return time.Time{}, true
	case reason == ReasonStreamReplaced, strings.HasPrefix(reason, ReasonTempBanPrefix), strings.HasPrefix(reason, ReasonConnectFailurePrefix):
		return now.Add(r.cfg.ParkFor), true
	}
	return time.Time{}, false
}

func (r *Runtime) parkedLocked(id uuid.UUID, now time.Time) bool {
	until, ok := r.parked[id]
	if !ok {
		return false
	}
	if until.IsZero() || now.Before(until) {
		return true
	}
	delete(r.parked, id)
	return false
}

func (r *Runtime) forgetLocked(id uuid.UUID) {
	delete(r.owned, id)
	delete(r.parked, id)
	delete(r.openErr, id)
}

func (r *Runtime) retryableLocked(now time.Time) []Lease {
	out := make([]Lease, 0, len(r.owned))
	for id, l := range r.owned {
		if r.busyLocked(id) || r.parkedLocked(id, now) {
			continue
		}
		out = append(out, l)
	}
	return out
}

func (r *Runtime) busyLocked(id uuid.UUID) bool {
	if _, ok := r.held[id]; ok {
		return true
	}
	if _, ok := r.unlinking[id]; ok {
		return true
	}
	a, ok := r.linking[id]
	if !ok {
		return false
	}
	switch a.state.Outcome {
	case OutcomeTimeout, OutcomeFailed:
		delete(r.linking, id)
		return false
	case OutcomeConnected:
		return false
	}
	return true
}

// SECURITY: callers hold r.mu and change the maps in the same hold; spawnLocked refuses once shutdown began, so no wg.Add races the final wg.Wait.
func (r *Runtime) spawnLocked(fn func()) bool {
	if r.closing {
		return false
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		fn()
	}()
	return true
}

func (r *Runtime) shutdown(ctx context.Context) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownBudget)
	defer cancel()

	r.mu.Lock()
	r.closing = true
	hs := slices.Collect(maps.Values(r.held))
	clear(r.held)
	attempts := slices.Collect(maps.Values(r.linking))
	r.mu.Unlock()

	for _, a := range attempts {
		a.cancel()
	}

	done := make(chan struct{})
	// NOTE: detached on purpose; an engine Close that ignores ctx cannot be interrupted, so this goroutine ends when the last Close returns and never touches shared state after.
	go func() {
		r.closeAll(sctx, hs, "")
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-sctx.Done():
		r.log.WarnContext(ctx, "whatsapp: shutdown budget spent before every runtime goroutine stopped")
	}

	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), releaseBudget)
	defer rcancel()
	if err := r.leases.ReleaseAll(rctx, r.cfg.Owner); err != nil {
		r.log.WarnContext(rctx, "whatsapp: lease release on shutdown failed", "err", err)
	}
}

func (r *Runtime) closeAll(ctx context.Context, hs []*held, reason string) {
	var g errgroup.Group
	g.SetLimit(closeParallel)
	for _, h := range hs {
		if h.sess == nil {
			continue
		}
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(ctx, closeBudget)
			defer cancel()
			if err := h.sess.Close(cctx); err != nil {
				r.log.WarnContext(cctx, "whatsapp: session close failed", "device_id", h.ref.DeviceID, "err", err)
			}
			return nil
		})
	}
	_ = g.Wait()
	if reason == "" {
		return
	}
	for _, h := range hs {
		if h.sess != nil {
			r.emit(h.ref, StateDisconnected, reason)
		}
	}
}

func (r *Runtime) closeSession(s EngineSession) {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeBudget)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		r.log.WarnContext(ctx, "whatsapp: session close failed", "err", err)
	}
}

func (r *Runtime) emit(ref SessionRef, st State, reason string) {
	r.subscriber().OnState(ref, st, reason)
}

func (r *Runtime) subscriber() EventSink {
	if r.sink == nil {
		return nopSink{}
	}
	return r.sink
}

func engineErr(op string, err error) error {
	if IsEngineError(err) || IsNotConnectedError(err) || IsUnsupportedError(err) || IsInvalidPhoneError(err) || IsSessionGoneError(err) {
		return err
	}
	return &EngineError{Op: op, Err: err}
}

type routedSink struct {
	r  *Runtime
	id uuid.UUID
}

func (s routedSink) OnState(ref SessionRef, st State, reason string) {
	s.r.onEngineState(s.id, ref, st, reason)
}
func (s routedSink) OnDegraded(ref SessionRef, err error)         { s.r.subscriber().OnDegraded(ref, err) }
func (s routedSink) OnLinked(ref SessionRef, id Identity)         { s.r.onLinked(s.id, ref, id) }
func (s routedSink) OnMessage(ref SessionRef, m InboundMessage)   { s.r.subscriber().OnMessage(ref, m) }
func (s routedSink) OnReceipt(ref SessionRef, rc Receipt)         { s.r.subscriber().OnReceipt(ref, rc) }
func (s routedSink) OnContact(ref SessionRef, c ContactUpdate)    { s.r.subscriber().OnContact(ref, c) }
func (s routedSink) OnHistory(ref SessionRef, b []InboundMessage) { s.r.subscriber().OnHistory(ref, b) }

type nopSink struct{}

func (nopSink) OnState(SessionRef, State, string)      {}
func (nopSink) OnDegraded(SessionRef, error)           {}
func (nopSink) OnLinked(SessionRef, Identity)          {}
func (nopSink) OnMessage(SessionRef, InboundMessage)   {}
func (nopSink) OnReceipt(SessionRef, Receipt)          {}
func (nopSink) OnContact(SessionRef, ContactUpdate)    {}
func (nopSink) OnHistory(SessionRef, []InboundMessage) {}
