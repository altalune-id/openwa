package controlplane

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	apikeyv1 "altalune.id/template/gen/go/apikey/v1"
	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apikey"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
)

// APIKeyService implements apikey.v1.APIKeyService.
type APIKeyService struct {
	keys     *apikey.Service
	projects *project.Service
}

// NewAPIKeyService binds the handler to its collaborators.
func NewAPIKeyService(keys *apikey.Service, projects *project.Service) *APIKeyService {
	return &APIKeyService{keys: keys, projects: projects}
}

// List returns the keys in the request's project, never carrying a plaintext secret.
func (s *APIKeyService) List(ctx context.Context, req *connect.Request[apikeyv1.ListRequest]) (*connect.Response[apikeyv1.ListResponse], error) {
	tctx, pid, err := s.scopeToProject(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	items, err := s.keys.List(tctx, pid)
	if err != nil {
		return nil, translateKeyErr(err)
	}
	resp := &apikeyv1.ListResponse{Keys: make([]*apikeyv1.APIKey, 0, len(items))}
	for _, k := range items {
		resp.Keys = append(resp.Keys, keyToProto(k))
	}
	return connect.NewResponse(resp), nil
}

// Create mints a new key in the request's project. SECURITY: the plaintext secret is returned here only and cannot be recovered afterwards.
func (s *APIKeyService) Create(ctx context.Context, req *connect.Request[apikeyv1.CreateRequest]) (*connect.Response[apikeyv1.CreateResponse], error) {
	tctx, _, err := s.scopeToProject(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, validationErr("name", "name is required")
	}
	resourceIDs, err := parseUUIDs("resource_ids", req.Msg.GetResourceIds())
	if err != nil {
		return nil, err
	}
	expiresAt, err := optionalTimestamp("expires_at", req.Msg.GetExpiresAt())
	if err != nil {
		return nil, err
	}
	k, plaintext, err := s.keys.Mint(tctx, name, req.Msg.GetScopes(), resourceIDs, expiresAt)
	if err != nil {
		return nil, translateKeyErr(err)
	}
	return connect.NewResponse(&apikeyv1.CreateResponse{
		Key:       keyToProto(k),
		Plaintext: plaintext,
	}), nil
}

// Revoke marks the referenced key permanently unusable. SECURITY: a key outside the request's own project reports the same NotFound as a made-up id.
func (s *APIKeyService) Revoke(ctx context.Context, req *connect.Request[apikeyv1.RevokeRequest]) (*connect.Response[apikeyv1.RevokeResponse], error) {
	tctx, _, err := s.scopeToProject(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	kid, err := parseUUID("id", req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.keys.Revoke(tctx, kid); err != nil {
		return nil, translateKeyErr(err)
	}
	return connect.NewResponse(&apikeyv1.RevokeResponse{}), nil
}

func (s *APIKeyService) scopeToProject(ctx context.Context, projectIDRaw string) (context.Context, uuid.UUID, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, uuid.Nil, err
	}
	pid, err := parseUUID("project_id", projectIDRaw)
	if err != nil {
		return nil, uuid.Nil, err
	}
	scoped := tenant.Into(ctx, tenant.Context{OrgID: p.ActiveOrgID, UserID: p.UserID})
	proj, err := s.projects.ByID(scoped, pid)
	if err != nil {
		return nil, uuid.Nil, err
	}
	if proj.OrgID != p.ActiveOrgID {
		return nil, uuid.Nil, forbiddenErr("project belongs to another org", "project_id", pid.String())
	}
	tctx := tenant.Into(ctx, tenant.Context{
		OrgID:     proj.OrgID,
		ProjectID: proj.ID,
		UserID:    p.UserID,
	})
	return tctx, proj.ID, nil
}

// NOTE: internal/apikey/errors.go implements no ToAppError hop, so without this an unknown scope surfaces as internal.
func translateKeyErr(err error) error {
	var unknown *apikey.UnknownScopeError
	if errors.As(err, &unknown) {
		return validationErr("scopes", "unknown scope: "+unknown.Scope)
	}
	if apikey.IsNotFoundError(err) {
		return keyNotFoundErr()
	}
	return err
}

func keyNotFoundErr() error {
	return apperror.New(
		apperror.CodeNotFound,
		"API key not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeNotFound},
	)
}

func optionalTimestamp(field string, ts *timestamppb.Timestamp) (*time.Time, error) {
	if ts == nil {
		return nil, nil
	}
	if err := ts.CheckValid(); err != nil {
		return nil, apperror.New(
			apperror.CodeValidation,
			field+" is not a valid timestamp",
			codes.InvalidArgument,
			&apperrorv1.ErrorDetail{
				Code: apperror.CodeValidation,
				Meta: map[string]string{"field": field},
			},
		).WithCause(err)
	}
	t := ts.AsTime()
	return &t, nil
}

func keyToProto(k *apikey.APIKey) *apikeyv1.APIKey {
	msg := &apikeyv1.APIKey{
		Id:          k.ID.String(),
		ProjectId:   k.ProjectID.String(),
		Name:        k.Name,
		Scopes:      append([]string(nil), k.Scopes...),
		ResourceIds: uuidsToStrings(k.ResourceIDs),
		CreatedAt:   timestamppb.New(k.CreatedAt),
	}
	if k.ExpiresAt != nil {
		msg.ExpiresAt = timestamppb.New(*k.ExpiresAt)
	}
	if k.RevokedAt != nil {
		msg.RevokedAt = timestamppb.New(*k.RevokedAt)
	}
	if k.LastUsedAt != nil {
		msg.LastUsedAt = timestamppb.New(*k.LastUsedAt)
	}
	return msg
}

func uuidsToStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
