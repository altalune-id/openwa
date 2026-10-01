package boot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/httpclient"
	"altalune.id/openwa/internal/message"
)

func TestMediaFetcher_FetchesWithinTheLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-bytes"))
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 64)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f := newMediaFetcher(32, httpclient.WithAllowPrivateHosts(true))

	body, mime, err := f.Fetch(t.Context(), srv.URL+"/ok.png")
	require.NoError(t, err)
	require.Equal(t, "png-bytes", string(body))
	require.Equal(t, "image/png", mime)

	_, _, err = f.Fetch(t.Context(), srv.URL+"/big")
	require.True(t, message.IsMediaTooLargeError(err), "got %v", err)

	_, _, err = f.Fetch(t.Context(), srv.URL+"/missing")
	require.True(t, message.IsMediaFetchError(err))

	_, _, err = f.Fetch(t.Context(), "file:///etc/passwd")
	require.True(t, message.IsMediaFetchError(err))
}

// SECURITY: the production fetcher must refuse loopback and private destinations.
func TestMediaFetcher_RefusesPrivateHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	t.Cleanup(srv.Close)
	_, _, err := newMediaFetcher(1024).Fetch(t.Context(), srv.URL)
	require.True(t, message.IsMediaFetchError(err), "got %v", err)
	require.ErrorContains(t, err, "private")
}
