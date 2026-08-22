package webhook

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
)

// WithAuth wraps next with the authentication auth describes. A nil auth
// (or Type none/"") returns next unwrapped.
func WithAuth(auth *config.WebhookAuthConfig, next http.Handler) http.Handler {
	if auth == nil {
		return next
	}

	switch auth.Type {
	case config.AuthBasic:
		return basicAuth(auth.Username, auth.Password, next)
	case config.AuthBearer:
		return bearerAuth(auth.Token, next)
	default:
		return next
	}
}

func basicAuth(username, password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || !constantTimeEqual(user, username) || !constantTimeEqual(pass, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="nrdp-webhook"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !constantTimeEqual(got, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
