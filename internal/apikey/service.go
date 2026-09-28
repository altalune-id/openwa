package apikey

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/authn"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/template/internal/apikey")

// Service is the API key driving port.
type Service struct {
	store      Store
	scheme     Scheme
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	now        func() time.Time
}

// NewService binds the service to its dependencies.
func NewService(store Store, scheme Scheme, log *slog.Logger, unexpected apperror.UnexpectedFunc) *Service {
	return &Service{
		store:      store,
		scheme:     scheme,
		log:        log.With("module", "apikey"),
		unexpected: unexpected,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Mint creates a new key in the caller's tenant scope and returns it with its one-time plaintext secret.
func (s *Service) Mint(ctx context.Context, name string, scopes []string, resourceIDs []uuid.UUID, expiresAt *time.Time) (*APIKey, string, error) {
	ctx, span := tracer.Start(ctx, "apikey.Mint")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, "", err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	k, plaintext, err := s.scheme.Mint(tc.OrgID, tc.ProjectID, name, scopes, resourceIDs, expiresAt, s.now())
	if err != nil {
		span.RecordError(err)
		return nil, "", err
	}
	if saveErr := s.store.Save(ctx, k); saveErr != nil {
		span.RecordError(saveErr)
		return nil, "", s.unexpected(ctx, "apikey.Mint: save", saveErr,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	span.SetAttributes(attribute.String("apikey.id", k.ID.String()))
	return k, plaintext, nil
}

// List returns the keys in projectID within the caller's tenant scope.
func (s *Service) List(ctx context.Context, projectID uuid.UUID) ([]*APIKey, error) {
	ctx, span := tracer.Start(ctx, "apikey.List")
	defer span.End()
	span.SetAttributes(attribute.String("project_id", projectID.String()))

	out, err := s.store.List(ctx, projectID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "apikey.List: list", err, "project_id", projectID)
	}
	return out, nil
}

// Revoke marks a key permanently unusable in the caller's tenant scope.
func (s *Service) Revoke(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "apikey.Revoke")
	defer span.End()
	span.SetAttributes(attribute.String("apikey.id", id.String()))

	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	k, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "apikey.Revoke: load", err, "apikey_id", id)
	}
	// SECURITY: a key from another project or org reports the same *NotFoundError as a missing id.
	if k.OrgID != tc.OrgID || k.ProjectID != tc.ProjectID {
		return &NotFoundError{}
	}
	if k.RevokedAt == nil {
		now := s.now()
		k.RevokedAt = &now
	}
	if saveErr := s.store.Save(ctx, k); saveErr != nil {
		span.RecordError(saveErr)
		return s.unexpected(ctx, "apikey.Revoke: save", saveErr, "apikey_id", id)
	}
	return nil
}

// Authenticator turns a raw API key credential into a session.Principal.
type Authenticator struct {
	store  Store
	usage  *UsageWorker
	scheme Scheme
}

// NewAuthenticator binds an Authenticator to its store, usage recorder and key scheme. usage may be nil.
func NewAuthenticator(s Store, u *UsageWorker, scheme Scheme) *Authenticator {
	return &Authenticator{store: s, usage: u, scheme: scheme}
}

// Scheme returns the key scheme this Authenticator resolves, so a surface gate never restates it.
func (a *Authenticator) Scheme() Scheme { return a.scheme }

var _ authn.Authenticator = (*Authenticator)(nil)

// Authenticate implements authn.Authenticator.
func (a *Authenticator) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	return principalFor(k), nil
}

// Authorize resolves raw and additionally requires scope on resourceID within orgID/projectID.
func (a *Authenticator) Authorize(ctx context.Context, raw, scope string, orgID, projectID, resourceID uuid.UUID) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	if k.OrgID != orgID || k.ProjectID != projectID {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	if !k.Allows(scope, resourceID) {
		return session.Principal{}, &authn.InsufficientScopeError{Scope: scope}
	}
	return principalFor(k), nil
}

// AuthorizeProject resolves raw and requires an unrestricted project-wide grant of scope.
func (a *Authenticator) AuthorizeProject(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	if k.OrgID != orgID || k.ProjectID != projectID {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	if !k.AllowsProject(scope) {
		return session.Principal{}, &authn.InsufficientScopeError{Scope: scope}
	}
	return principalFor(k), nil
}

// SECURITY: every rejection collapses to one opaque *authn.UnauthorizedError; the shape gate short-circuits on purpose because the prefix is public config, and equalizing it would buy a DoS amplifier (see BACKLOG).
func (a *Authenticator) resolve(ctx context.Context, raw string) (*APIKey, error) {
	if a.scheme.Authn().Looks(raw) != authn.ShapeAPIKey {
		return nil, &authn.UnauthorizedError{}
	}
	sum := sha256.Sum256([]byte(raw))
	k, err := a.store.BySecretHash(ctx, sum)
	if err != nil || k == nil || !k.Usable(time.Now().UTC()) {
		return nil, &authn.UnauthorizedError{}
	}
	if a.usage != nil {
		a.usage.Record(k.ID, tenant.Context{OrgID: k.OrgID, ProjectID: k.ProjectID}, time.Now().UTC())
	}
	return k, nil
}

// SECURITY: a key is not a person, so UserID stays nil and no secret travels with the principal.
func principalFor(k *APIKey) session.Principal {
	return session.Principal{
		UserID:          uuid.Nil,
		KeyID:           k.ID,
		Name:            k.Name,
		Source:          session.SourceAPIKey,
		Scopes:          slices.Clone(k.Scopes),
		ActiveOrgID:     k.OrgID,
		ActiveProjectID: k.ProjectID,
		IssuedAt:        k.CreatedAt,
	}
}
