package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
)

// okHandler records whether the wrapped handler was reached at all, which
// is the property that actually matters: a middleware bug that lets a
// request through is invisible in the status code alone.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestWithAuthNilAndNonePassThrough(t *testing.T) {
	for name, auth := range map[string]*config.WebhookAuthConfig{
		"nil":        nil,
		"none":       {Type: config.AuthNone},
		"empty type": {Type: ""},
	} {
		t.Run(name, func(t *testing.T) {
			var reached bool
			rec := httptest.NewRecorder()
			WithAuth(auth, okHandler(&reached)).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhook", nil))
			if !reached || rec.Code != http.StatusOK {
				t.Errorf("reached=%v status=%d, want the request to pass through", reached, rec.Code)
			}
		})
	}
}

func TestWithAuthBasic(t *testing.T) {
	auth := &config.WebhookAuthConfig{Type: config.AuthBasic, Username: "am", Password: "s3cret"}

	tests := []struct {
		name       string
		setup      func(*http.Request)
		wantPassed bool
	}{
		{"correct credentials", func(r *http.Request) { r.SetBasicAuth("am", "s3cret") }, true},
		{"wrong password", func(r *http.Request) { r.SetBasicAuth("am", "nope") }, false},
		{"wrong username", func(r *http.Request) { r.SetBasicAuth("nope", "s3cret") }, false},
		{"no header at all", func(*http.Request) {}, false},
		{"bearer instead of basic", func(r *http.Request) { r.Header.Set("Authorization", "Bearer s3cret") }, false},
		// Guards against a prefix/substring comparison creeping in.
		{"password is a prefix", func(r *http.Request) { r.SetBasicAuth("am", "s3cr") }, false},
		{"password has a suffix", func(r *http.Request) { r.SetBasicAuth("am", "s3cretX") }, false},
		{"empty credentials", func(r *http.Request) { r.SetBasicAuth("", "") }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reached bool
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			tt.setup(req)
			rec := httptest.NewRecorder()
			WithAuth(auth, okHandler(&reached)).ServeHTTP(rec, req)

			if reached != tt.wantPassed {
				t.Fatalf("handler reached = %v, want %v", reached, tt.wantPassed)
			}
			if !tt.wantPassed {
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("status = %d, want 401", rec.Code)
				}
				if rec.Header().Get("WWW-Authenticate") == "" {
					t.Error("missing WWW-Authenticate header on a 401")
				}
			}
		})
	}
}

func TestWithAuthBearer(t *testing.T) {
	auth := &config.WebhookAuthConfig{Type: config.AuthBearer, Token: "tok123"}

	tests := []struct {
		name       string
		header     string
		wantPassed bool
	}{
		{"correct token", "Bearer tok123", true},
		{"wrong token", "Bearer nope", false},
		{"no header", "", false},
		{"missing Bearer prefix", "tok123", false},
		{"wrong scheme", "Basic tok123", false},
		{"lowercase scheme", "bearer tok123", false},
		{"token is a prefix", "Bearer tok", false},
		{"token has a suffix", "Bearer tok123X", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reached bool
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			WithAuth(auth, okHandler(&reached)).ServeHTTP(rec, req)

			if reached != tt.wantPassed {
				t.Fatalf("handler reached = %v, want %v", reached, tt.wantPassed)
			}
			if !tt.wantPassed && rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}
}
