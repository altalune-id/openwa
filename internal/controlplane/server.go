// Package controlplane wires the Connect-RPC handlers into an http.Handler.
package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"

	apikeyv1 "altalune.id/template/gen/go/apikey/v1"
	apikeyv1connect "altalune.id/template/gen/go/apikey/v1/apikeyv1connect"
	authv1 "altalune.id/template/gen/go/auth/v1"
	authv1connect "altalune.id/template/gen/go/auth/v1/authv1connect"
	blogv1 "altalune.id/template/gen/go/blog/v1"
	blogv1connect "altalune.id/template/gen/go/blog/v1/blogv1connect"
	orgv1 "altalune.id/template/gen/go/org/v1"
	orgv1connect "altalune.id/template/gen/go/org/v1/orgv1connect"
	projectv1 "altalune.id/template/gen/go/project/v1"
	projectv1connect "altalune.id/template/gen/go/project/v1/projectv1connect"
	todov1 "altalune.id/template/gen/go/todo/v1"
	todov1connect "altalune.id/template/gen/go/todo/v1/todov1connect"
	"altalune.id/template/internal/apikey"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/blog"
	"altalune.id/template/internal/blog/category"
	"altalune.id/template/internal/blog/tag"
	"altalune.id/template/internal/controlplane/interceptor"
	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform"
	"altalune.id/template/internal/platform/authn"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/todo"
	"altalune.id/template/internal/user"
)

// Server holds the wired Connect handlers and their runtime configuration.
type Server struct {
	Cfg    *config.Config
	Kernel *platform.Kernel

	Auths    *auth.Service
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	Todos    *todo.Service
	Invites  *invite.Service

	Posts      *blog.Service
	Categories *category.Service
	Tags       *tag.Service

	// APIKeys is set by boot after New returns, mirroring Authn/KeyPrefix below.
	APIKeys *apikey.Service

	AuthSvc    *AuthService
	TodoSvc    *TodoService
	BlogSvc    *BlogService
	ProjectSvc *ProjectService
	MemberSvc  *MemberService
	APIKeySvc  *APIKeyService

	Authn     authn.Chain
	KeyPrefix string

	OpenAPIEnabled   bool
	OpenAPIBasicAuth *BasicAuth
}

// New builds a Server from every domain service.
func New(
	cfg *config.Config,
	kernel *platform.Kernel,
	auths *auth.Service,
	users *user.Service,
	orgs *org.Service,
	projects *project.Service,
	todos *todo.Service,
	invites *invite.Service,
	todoStore todo.Store,
	posts *blog.Service,
	categories *category.Service,
	tags *tag.Service,
) *Server {
	s := &Server{
		Cfg:      cfg,
		Kernel:   kernel,
		Auths:    auths,
		Users:    users,
		Orgs:     orgs,
		Projects: projects,
		Todos:    todos,
		Invites:  invites,

		Posts:      posts,
		Categories: categories,
		Tags:       tags,

		AuthSvc:    NewAuthService(orgs),
		TodoSvc:    NewTodoService(todos, todoStore, projects),
		BlogSvc:    NewBlogService(posts, categories, tags, projects),
		ProjectSvc: NewProjectService(projects),
		MemberSvc:  NewMemberService(orgs),
	}
	if cfg != nil {
		s.OpenAPIEnabled = cfg.API.OpenAPI.Enabled
		if cfg.API.OpenAPI.RequireBasicAuth {
			s.OpenAPIBasicAuth = &BasicAuth{
				User:     cfg.API.OpenAPI.BasicAuthUser,
				Password: cfg.API.OpenAPI.BasicAuthPassword,
			}
		}
	}
	return s
}

var (
	_ authv1connect.AuthServiceHandler       = (*AuthService)(nil)
	_ todov1connect.TodoServiceHandler       = (*TodoService)(nil)
	_ blogv1connect.BlogServiceHandler       = (*BlogService)(nil)
	_ projectv1connect.ProjectServiceHandler = (*ProjectService)(nil)
	_ orgv1connect.MemberServiceHandler      = (*MemberService)(nil)
	_ apikeyv1connect.APIKeyServiceHandler   = (*APIKeyService)(nil)
)

// Handler mounts the Connect handlers plus OpenAPI endpoints under basePath+"/api".
func (s *Server) Handler(basePath string) http.Handler {
	opts := s.handlerOptions()
	// NOTE: built here, not in New, since s.APIKeys isn't set until after New returns.
	s.APIKeySvc = NewAPIKeyService(s.APIKeys, s.Projects)

	inner := http.NewServeMux()
	todoPath, todoHandler := todov1connect.NewTodoServiceHandler(s.TodoSvc, opts...)
	inner.Handle(todoPath, todoHandler)
	authPath, authHandler := authv1connect.NewAuthServiceHandler(s.AuthSvc, opts...)
	inner.Handle(authPath, authHandler)
	blogPath, blogHandler := blogv1connect.NewBlogServiceHandler(s.BlogSvc, opts...)
	inner.Handle(blogPath, blogHandler)
	projectPath, projectHandler := projectv1connect.NewProjectServiceHandler(s.ProjectSvc, opts...)
	inner.Handle(projectPath, projectHandler)
	memberPath, memberHandler := orgv1connect.NewMemberServiceHandler(s.MemberSvc, opts...)
	inner.Handle(memberPath, memberHandler)
	apikeyPath, apikeyHandler := apikeyv1connect.NewAPIKeyServiceHandler(s.APIKeySvc, opts...)
	inner.Handle(apikeyPath, apikeyHandler)

	if s.OpenAPIEnabled {
		yamlBody, jsonBody := openAPI()
		if len(yamlBody) > 0 {
			guard := openAPIGuard(s.OpenAPIBasicAuth)
			inner.Handle("/openapi.yaml", s.recoverHTTP(guard(openAPIHandler(yamlBody, "application/yaml"))))
			inner.Handle("/openapi.json", s.recoverHTTP(guard(openAPIHandler(jsonBody, "application/json"))))
			inner.Handle("/docs", s.recoverHTTP(guard(docsHandler(basePath+"/api/openapi.yaml"))))
		}
	}

	mount := basePath + "/api"
	outer := http.NewServeMux()
	outer.Handle(mount+"/", http.StripPrefix(mount, inner))
	return outer
}

// MountedProcedures returns every Connect procedure path the handler serves.
func (s *Server) MountedProcedures() []string {
	return slices.Concat(
		serviceProcedures(todov1.File_todo_v1_todo_proto, "TodoService"),
		serviceProcedures(authv1.File_auth_v1_auth_proto, "AuthService"),
		serviceProcedures(blogv1.File_blog_v1_blog_proto, "BlogService"),
		serviceProcedures(projectv1.File_project_v1_project_proto, "ProjectService"),
		serviceProcedures(orgv1.File_org_v1_org_proto, "MemberService"),
		serviceProcedures(apikeyv1.File_apikey_v1_apikey_proto, "APIKeyService"),
	)
}

func serviceProcedures(file protoreflect.FileDescriptor, name string) []string {
	svc := file.Services().ByName(protoreflect.Name(name))
	methods := svc.Methods()
	procedures := make([]string, methods.Len())
	for i := range methods.Len() {
		procedures[i] = fmt.Sprintf("/%s/%s", svc.FullName(), methods.Get(i).Name())
	}
	return procedures
}

func (s *Server) handlerOptions() []connect.HandlerOption {
	ics := []connect.Interceptor{
		interceptor.RequestID(),
	}
	if otel, err := interceptor.OTel(nil, nil); err == nil && otel != nil {
		ics = append(ics, otel)
	}
	ics = append(ics,
		interceptor.Wrap(s.unexpected()),
		translateAuthnErrors(),
		authn.Interceptor(s.Authn, authn.Scheme{Prefix: s.KeyPrefix}, ScopeTable()),
		interceptor.Tenant(),
	)
	return []connect.HandlerOption{
		connect.WithRecover(s.recoverPanic),
		connect.WithInterceptors(ics...),
	}
}

func (s *Server) recoverPanic(ctx context.Context, _ connect.Spec, _ http.Header, p any) error {
	return interceptor.Translate(ctx, fmt.Errorf("panic: %v", p), s.unexpected())
}

func (s *Server) recoverHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			err := fmt.Errorf("panic: %v", p)
			if unexpected := s.unexpected(); unexpected != nil {
				_ = unexpected(r.Context(), "api: unexpected", err)
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) unexpected() apperror.UnexpectedFunc {
	if s.Kernel == nil || s.Kernel.Reporter == nil {
		return nil
	}
	return s.Kernel.Reporter.Unexpected
}
