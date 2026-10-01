package middleware

import (
	"context"
	"net/http"
	"strings"

	"altalune.id/openwa/internal/web"
)

type flashCtxKey struct{}

// Flash reads the signed flash cookie once per full-page navigation, clears it and stores the payload on the context. NOTE: htmx requests and basePath/static/ pass through so a poll or asset request cannot consume a flash meant for the next page.
func Flash(secret []byte, basePath string, secure bool) web.Middleware {
	staticPrefix := web.Path(basePath, "/static/")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if web.IsHTMXRequest(r) || strings.HasPrefix(r.URL.Path, staticPrefix) {
				next.ServeHTTP(w, r)
				return
			}
			c, err := r.Cookie(web.FlashCookieName)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			web.ClearCookie(w, web.FlashCookieName, basePath, secure)
			raw, err := web.VerifyCookie(secret, c.Value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			p, err := web.DecodeFlash(raw)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), flashCtxKey{}, p)))
		})
	}
}

// FlashFrom returns the flash payload the middleware stored, if any.
func FlashFrom(ctx context.Context) (web.FlashPayload, bool) {
	p, ok := ctx.Value(flashCtxKey{}).(web.FlashPayload)
	return p, ok
}
