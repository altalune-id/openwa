package boot

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"altalune.id/openwa/httpclient"
	"altalune.id/openwa/internal/message"
)

const mediaFetchTimeout = 30 * time.Second

// SECURITY: built on httpclient.New, whose dial filter refuses private and loopback addresses.
type httpFetcher struct {
	client *http.Client
	max    int64
}

var _ message.MediaFetcher = (*httpFetcher)(nil)

func newMediaFetcher(maxBytes int64, opts ...httpclient.Option) *httpFetcher {
	opts = append([]httpclient.Option{httpclient.WithTimeout(mediaFetchTimeout)}, opts...)
	return &httpFetcher{client: httpclient.New(opts...), max: maxBytes}
}

func (f *httpFetcher) Fetch(ctx context.Context, rawURL string) (body []byte, mimeType string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, "", &message.MediaFetchError{URL: rawURL, Reason: "not an http(s) URL"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, "", &message.MediaFetchError{URL: rawURL, Reason: "bad request"}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		if httpclient.IsPrivateAddressError(err) {
			return nil, "", &message.MediaFetchError{URL: rawURL, Reason: "private address refused"}
		}
		return nil, "", &message.MediaFetchError{URL: rawURL, Reason: "download failed"}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", &message.MediaFetchError{URL: rawURL, Reason: "HTTP " + resp.Status}
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, f.max+1))
	if err != nil {
		return nil, "", &message.MediaFetchError{URL: rawURL, Reason: "read failed"}
	}
	if int64(len(body)) > f.max {
		return nil, "", &message.MediaTooLargeError{Size: int64(len(body)), Max: f.max}
	}
	mimeType, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if parseErr != nil || mimeType == "" {
		mimeType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
	}
	return body, mimeType, nil
}
