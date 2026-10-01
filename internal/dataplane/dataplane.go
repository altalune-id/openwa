// Package dataplane implements S3, the REST data plane over blog posts and devices: the surface an integrator's running product calls directly, authenticated by API key.
package dataplane

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/platform/capabilities"
	"altalune.id/openwa/internal/platform/session"
)

// DefaultMaxConcurrentSends bounds the sends in flight, each of which can hold several times whatsapp.mediaMaxBytes in memory.
const DefaultMaxConcurrentSends = 4

// Posts is the driven port the data plane reads posts through.
type Posts interface {
	BySlug(ctx context.Context, projectID uuid.UUID, slug string) (PostRef, error)
	List(ctx context.Context, orgID, projectID uuid.UUID, opts ListOpts) ([]PostRef, error)
	Create(ctx context.Context, categoryID uuid.UUID, title, slug, body string) (PostRef, error)
	Update(ctx context.Context, id uuid.UUID, title, slug, body string, categoryID uuid.UUID, ifVersion int) (PostRef, error)
	Delete(ctx context.Context, id uuid.UUID, ifVersion int) error
	Publish(ctx context.Context, id uuid.UUID, ifVersion int) (PostRef, error)
	Unpublish(ctx context.Context, id uuid.UUID, ifVersion int) (PostRef, error)
}

// Devices is the driven port the data plane reaches devices through.
type Devices interface {
	List(ctx context.Context) ([]DeviceRef, error)
	// Resolve returns the caller's project's device whose public id is publicID.
	Resolve(ctx context.Context, publicID string) (DeviceRef, error)
	Get(ctx context.Context, id uuid.UUID) (DeviceRef, error)
	Create(ctx context.Context, name string) (DeviceRef, error)
	Update(ctx context.Context, id uuid.UUID, name *string, rules *RulesRef, ifVersion int) (DeviceRef, error)
	Delete(ctx context.Context, id uuid.UUID) error
	StartLink(ctx context.Context, id uuid.UUID) (LinkRef, error)
	LinkWithPhone(ctx context.Context, id uuid.UUID, phone string) (LinkRef, error)
	LinkState(ctx context.Context, id uuid.UUID) (LinkRef, error)
	Unlink(ctx context.Context, id uuid.UUID) error
}

// RulesRef is a device's inbound rules as this surface carries them.
type RulesRef struct {
	GroupMode      string
	AllowedSenders []string
	AllowedGroups  []string
	TriggerPrefix  string
	IgnoreFromMe   bool
}

// DeviceRef is the device this surface needs, with its session status; only PublicID is ever written to a response.
type DeviceRef struct {
	ID         uuid.UUID
	PublicID   string
	Name       string
	Rules      RulesRef
	Version    int
	State      string
	Phone      string
	PushName   string
	LastSeenAt *time.Time
}

// LinkRef is one pairing attempt; Outcome "none" means there is none, and ID is its lnk_ public id.
type LinkRef struct {
	ID          string
	Method      string
	Outcome     string
	QR          string
	PNG         []byte
	PairingCode string
	ExpiresAt   time.Time
	StartedAt   time.Time
}

// ListOpts filters a collection read, with PublishedOnly set for an uncredentialed caller.
type ListOpts struct {
	PublishedOnly bool
}

// PostRef is the post this surface needs, referenced across the module boundary by id.
type PostRef struct {
	ID         uuid.UUID
	CategoryID uuid.UUID
	Title      string
	Slug       string
	Body       string
	Published  bool
	Version    int
}

// Authorizer authenticates a raw credential and authorizes it against a scope and tenant.
type Authorizer interface {
	Authenticate(ctx context.Context, raw string) (session.Principal, error)
	Authorize(ctx context.Context, raw, scope string, orgID, projectID, resourceID uuid.UUID) (session.Principal, error)
	AuthorizeProject(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error)
	AuthorizeScope(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error)
}

// Capabilities is the feature-flag snapshot the data plane checks, such as public reads.
type Capabilities = capabilities.Capabilities

// Handler serves S3, the REST data plane over blog posts and devices.
type Handler struct {
	mux      *http.ServeMux
	resolver resolver
	posts    Posts
	devices  Devices
	messages Messages
	chats    Chats
	contacts Contacts
	maxSend  int64
	sendSlot chan struct{}
	basePath string
	authz    Authorizer
	caps     Capabilities
	idem     *idempotencyStore
	log      *slog.Logger
}

// ServeHTTP implements http.Handler. NOTE: R6 fixes the error shape, so an unrouted path answers with the declared envelope, not the mux default.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, pattern := h.mux.Handler(r); pattern != "" {
		h.mux.ServeHTTP(w, r)
		return
	}
	allowed := h.allowedMethods(r)
	if len(allowed) == 0 {
		h.fail(w, r, &NotFoundError{})
		return
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	h.fail(w, r, &MethodNotAllowedError{})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if statusFor(err).status >= http.StatusInternalServerError {
		h.log.ErrorContext(r.Context(), "data plane request failed",
			"method", r.Method, "path", r.URL.Path, "error", err)
	}
	writeError(w, err)
}

func (h *Handler) allowedMethods(r *http.Request) []string {
	routable := []string{
		http.MethodGet, http.MethodHead, http.MethodPost,
		http.MethodPut, http.MethodPatch, http.MethodDelete,
	}
	allowed := make([]string, 0, len(routable))
	for _, method := range routable {
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := h.mux.Handler(probe); pattern != "" {
			allowed = append(allowed, method)
		}
	}
	return allowed
}

// HandlerParams collects Handler's dependencies.
type HandlerParams struct {
	BasePath string
	Orgs     Orgs
	Projects Projects
	Posts    Posts
	Devices  Devices
	Authz    Authorizer
	Caps     Capabilities
	Log      *slog.Logger

	Messages      Messages
	Chats         Chats
	Contacts      Contacts
	MaxMediaBytes int64
	// MaxConcurrentSends bounds sends in flight; zero means DefaultMaxConcurrentSends.
	MaxConcurrentSends int
}

// NewHandler builds the data plane's REST handler, mounted under BasePath.
func NewHandler(p HandlerParams) http.Handler {
	log := p.Log
	if log == nil {
		log = slog.Default()
	}

	h := &Handler{
		mux:      http.NewServeMux(),
		resolver: resolver{orgs: p.Orgs, projects: p.Projects},
		posts:    p.Posts,
		devices:  p.Devices,
		messages: p.Messages,
		chats:    p.Chats,
		contacts: p.Contacts,
		maxSend:  p.MaxMediaBytes*4/3 + 1<<20,
		sendSlot: make(chan struct{}, cmp.Or(p.MaxConcurrentSends, DefaultMaxConcurrentSends)),
		basePath: p.BasePath,
		authz:    p.Authz,
		caps:     p.Caps,
		idem:     newIdempotencyStore(IdempotencyTTL, IdempotencyMaxEntries),
		log:      log.With("module", "dataplane"),
	}

	posts := p.BasePath + "/orgs/{org}/projects/{project}/posts"
	h.mux.HandleFunc("GET "+posts, h.listPosts)
	h.mux.HandleFunc("GET "+posts+"/{slug}", h.getPost)
	h.mux.HandleFunc("POST "+posts, h.createPost)
	h.mux.HandleFunc("PUT "+posts+"/{slug}", h.replacePost)
	h.mux.HandleFunc("PATCH "+posts+"/{slug}", h.patchPost)
	h.mux.HandleFunc("DELETE "+posts+"/{slug}", h.deletePost)
	h.mux.HandleFunc("POST "+posts+"/{slug}/publish", h.publishPost)
	h.mux.HandleFunc("POST "+posts+"/{slug}/unpublish", h.unpublishPost)

	base := p.BasePath + "/orgs/{org}/projects/{project}"
	h.mux.HandleFunc("GET "+base+"/devices", h.listDevices)
	h.mux.HandleFunc("POST "+base+"/devices", h.createDevice)
	h.mux.HandleFunc("GET "+base+"/devices/{device}", h.getDevice)
	h.mux.HandleFunc("PATCH "+base+"/devices/{device}", h.patchDevice)
	h.mux.HandleFunc("DELETE "+base+"/devices/{device}", h.deleteDevice)
	h.mux.HandleFunc("POST "+base+"/devices/{device}/links", h.createLink)
	h.mux.HandleFunc("GET "+base+"/devices/{device}/links/current", h.getLink)
	h.mux.HandleFunc("POST "+base+"/devices/{device}/unlink", h.unlinkDevice)

	h.mux.HandleFunc("POST "+base+"/devices/{device}/messages", h.sendMessage)
	h.mux.HandleFunc("GET "+base+"/devices/{device}/messages", h.listDeviceMessages)
	h.mux.HandleFunc("GET "+base+"/messages/{message}", h.getMessage)
	h.mux.HandleFunc("GET "+base+"/messages/{message}/media", h.getMedia)
	h.mux.HandleFunc("POST "+base+"/messages/{message}/reactions", h.reactMessage)
	h.mux.HandleFunc("POST "+base+"/messages/{message}/revoke", h.revokeMessage)
	h.mux.HandleFunc("PATCH "+base+"/messages/{message}", h.editMessage)
	h.mux.HandleFunc("POST "+base+"/messages/{message}/read", h.readMessage)
	h.mux.HandleFunc("GET "+base+"/chats", h.listChats)
	h.mux.HandleFunc("GET "+base+"/chats/{chat}", h.getChat)
	h.mux.HandleFunc("GET "+base+"/chats/{chat}/messages", h.listChatMessages)
	h.mux.HandleFunc("POST "+base+"/chats/{chat}/read", h.readChat)
	h.mux.HandleFunc("GET "+base+"/chats/{chat}/group", h.getGroup)
	h.mux.HandleFunc("POST "+base+"/chats/{chat}/leave", h.leaveGroup)
	h.mux.HandleFunc("POST "+base+"/devices/{device}/groups", h.joinGroup)
	h.mux.HandleFunc("GET "+base+"/contacts", h.listContacts)

	return h
}
