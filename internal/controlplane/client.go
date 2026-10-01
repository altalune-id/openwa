package controlplane

import (
	"context"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	authv1connect "altalune.id/openwa/gen/go/auth/v1/authv1connect"
	blogv1connect "altalune.id/openwa/gen/go/blog/v1/blogv1connect"
	chatv1connect "altalune.id/openwa/gen/go/chat/v1/chatv1connect"
	contactv1connect "altalune.id/openwa/gen/go/contact/v1/contactv1connect"
	devicev1connect "altalune.id/openwa/gen/go/device/v1/devicev1connect"
	messagev1connect "altalune.id/openwa/gen/go/message/v1/messagev1connect"
	todov1connect "altalune.id/openwa/gen/go/todo/v1/todov1connect"
)

// DefaultClientTimeout is applied to the http.Client used by NewClient.
const DefaultClientTimeout = 30 * time.Second

// Client bundles the Connect clients for every service published by controlplane.Server.
type Client struct {
	Auth   authv1connect.AuthServiceClient
	Todo   todov1connect.TodoServiceClient
	Blog   blogv1connect.BlogServiceClient
	Device devicev1connect.DeviceServiceClient

	Message messagev1connect.MessageServiceClient
	Chat    chatv1connect.ChatServiceClient
	Contact contactv1connect.ContactServiceClient
}

// NewClient builds a Client pointing at baseURL; a non-empty token becomes the Bearer header.
func NewClient(baseURL, token string) *Client {
	httpClient := &http.Client{Timeout: DefaultClientTimeout}
	base := strings.TrimRight(baseURL, "/") + "/api"
	opts := []connect.ClientOption{
		connect.WithInterceptors(bearerInterceptor(token)),
	}
	return &Client{
		Auth:   authv1connect.NewAuthServiceClient(httpClient, base, opts...),
		Todo:   todov1connect.NewTodoServiceClient(httpClient, base, opts...),
		Blog:   blogv1connect.NewBlogServiceClient(httpClient, base, opts...),
		Device: devicev1connect.NewDeviceServiceClient(httpClient, base, opts...),

		Message: messagev1connect.NewMessageServiceClient(httpClient, base, opts...),
		Chat:    chatv1connect.NewChatServiceClient(httpClient, base, opts...),
		Contact: contactv1connect.NewContactServiceClient(httpClient, base, opts...),
	}
}

func bearerInterceptor(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if token != "" {
				req.Header().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, req)
		}
	}
}
