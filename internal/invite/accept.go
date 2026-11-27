package invite

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/tenant"
)

type userStore interface {
	ByEmail(ctx context.Context, email string) (*UserRef, error)
	Save(ctx context.Context, u *UserRef) error
}

type orgStore interface {
	ByID(ctx context.Context, id uuid.UUID) (*OrgRef, error)
	SaveMembership(ctx context.Context, m *MembershipRef) error
	MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*MembershipRef, error)
}

// UserRef is the projection of the user aggregate consumed by AcceptWorkflow.
type UserRef struct {
	ID     uuid.UUID
	Email  string
	Name   string
	Source string
}

// OrgRef is the projection of the org aggregate consumed by AcceptWorkflow.
type OrgRef struct {
	ID   uuid.UUID
	Slug string
	Name string
}

// MembershipRef is the projection of an org membership row.
type MembershipRef struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
	Role   Role
}

// AcceptRequest is the input to AcceptWorkflow.Execute.
type AcceptRequest struct {
	Token string
	Email string
	Name  string
}

// AcceptResult carries the user and membership produced by acceptance.
type AcceptResult struct {
	Invite     *Invite
	User       *UserRef
	Membership *MembershipRef
}

// AcceptWorkflow redeems a raw token: verifies the invite, finds-or-creates the user, enrols the membership, and marks the invite consumed.
type AcceptWorkflow struct {
	invites    Store
	users      userStore
	orgs       orgStore
	uow        tenant.UnitOfWork
	clock      func() time.Time
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// AcceptOption configures an AcceptWorkflow.
type AcceptOption func(*AcceptWorkflow)

// WithAcceptClock overrides the workflow clock.
func WithAcceptClock(clock func() time.Time) AcceptOption {
	return func(w *AcceptWorkflow) { w.clock = clock }
}

// NewAcceptWorkflow wires the workflow.
func NewAcceptWorkflow(invites Store, users userStore, orgs orgStore, log *slog.Logger, unexpected apperror.UnexpectedFunc, uow tenant.UnitOfWork, opts ...AcceptOption) *AcceptWorkflow {
	w := &AcceptWorkflow{
		invites:    invites,
		users:      users,
		orgs:       orgs,
		uow:        uow,
		clock:      func() time.Time { return time.Now().UTC() },
		log:        log,
		unexpected: unexpected,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Execute redeems the raw token for the presented email.
func (w *AcceptWorkflow) Execute(ctx context.Context, req AcceptRequest) (*AcceptResult, error) {
	ctx, span := tracer.Start(ctx, "invite.Accept.Execute")
	defer span.End()

	if req.Token == "" {
		return nil, &TokenMismatchError{}
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return nil, err
	}

	inv, err := w.invites.ByTokenHash(ctx, HashToken(req.Token))
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, &TokenMismatchError{}
		}
		return nil, w.unexpected(ctx, "invite.Accept: byTokenHash", fmt.Errorf("invite.Accept: byTokenHash: %w", err))
	}
	span.SetAttributes(attribute.String("invite.id", inv.ID.String()))

	if !strings.EqualFold(inv.Email, email) {
		return nil, &TokenMismatchError{}
	}

	now := w.clock()
	if err := inv.Consume(now); err != nil {
		return nil, err
	}

	usr, err := w.ensureUser(ctx, email, req.Name)
	if err != nil {
		return nil, err
	}

	if _, err := w.orgs.ByID(ctx, inv.OrgID); err != nil {
		span.RecordError(err)
		return nil, w.unexpected(ctx, "invite.Accept: org lookup", fmt.Errorf("invite.Accept: org lookup: %w", err), "org_id", inv.OrgID)
	}

	// tenant-scope the writes to the invite's org — the acceptor isn't yet a member of any tenant so ctx has no tenant.Context on it.
	tctx := tenant.Into(ctx, tenant.Context{OrgID: inv.OrgID, UserID: usr.ID})
	var m *MembershipRef
	err = w.uow(tctx, func(ctx context.Context) error {
		var jErr error
		if m, jErr = w.join(ctx, inv.OrgID, usr.ID, inv.Role); jErr != nil {
			return jErr
		}
		if sErr := w.invites.Save(ctx, inv); sErr != nil {
			return w.unexpected(ctx, "invite.Accept: save invite", fmt.Errorf("invite.Accept: save invite: %w", sErr),
				"invite_id", inv.ID)
		}
		return nil
	})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	return &AcceptResult{Invite: inv, User: usr, Membership: m}, nil
}

// SECURITY: an existing membership is returned untouched; overwriting it would let an invite demote an owner.
func (w *AcceptWorkflow) join(ctx context.Context, orgID, userID uuid.UUID, role Role) (*MembershipRef, error) {
	existing, err := w.orgs.MembershipOf(ctx, orgID, userID)
	if err == nil {
		return existing, nil
	}
	if !IsMembershipMissingError(err) {
		return nil, w.unexpected(ctx, "invite.Accept: membership lookup", fmt.Errorf("invite.Accept: membership lookup: %w", err),
			"org_id", orgID, "user_id", userID)
	}
	m := &MembershipRef{OrgID: orgID, UserID: userID, Role: role}
	if err := w.orgs.SaveMembership(ctx, m); err != nil {
		return nil, w.unexpected(ctx, "invite.Accept: save membership", fmt.Errorf("invite.Accept: save membership: %w", err),
			"org_id", orgID, "user_id", userID)
	}
	return m, nil
}

func (w *AcceptWorkflow) ensureUser(ctx context.Context, email, name string) (*UserRef, error) {
	if usr, err := w.users.ByEmail(ctx, email); err == nil {
		return usr, nil
	}
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = defaultName(email)
	}
	usr := &UserRef{
		ID:     uuid.New(),
		Email:  email,
		Name:   displayName,
		Source: "invite",
	}
	if err := w.users.Save(ctx, usr); err != nil {
		return nil, w.unexpected(ctx, "invite.Accept: user save", fmt.Errorf("invite.Accept: user save: %w", err), "email", email)
	}
	return usr, nil
}

func defaultName(email string) string {
	if at := strings.IndexByte(email, '@'); at > 0 {
		return email[:at]
	}
	return email
}
