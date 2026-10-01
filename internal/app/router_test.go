package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Every v1 and v2 shared-book route must resolve to its own handler: the
// static "/shared/v2" segment lives next to v1's "/shared/:code" wildcard.
// With dbPool == nil, a resolved handler answers 500 "Database not connected"
// (or a handler-specific 400 when the request is rejected before the DB).
func TestSharedRoutesResolve(t *testing.T) {
	gin.SetMode(gin.TestMode)
	saved := dbPool
	dbPool = nil
	defer func() { dbPool = saved }()
	setupRouter()
	r := router

	cases := []struct {
		name, method, path, body string
		status                   int
		errContains              string
	}{
		{"v1 share", "POST", "/api/shared/share", `{"book":{}}`, 500, "Database not connected"},
		{"v1 get", "GET", "/api/shared/ABCDEFGH", "", 500, "Database not connected"},
		{"v1 put", "PUT", "/api/shared/ABCDEFGH", `{}`, 500, "Database not connected"},
		{"v1 get code starting with v2", "GET", "/api/shared/v2ABCDEF", "", 500, "Database not connected"},
		{"v1 put code starting with v2", "PUT", "/api/shared/v2ABCDEF", `{}`, 500, "Database not connected"},
		{"v2 create", "POST", "/api/shared/v2", `{"doc":{}}`, 500, "Database not connected"},
		{"v2 create validates", "POST", "/api/shared/v2", `{}`, 400, "doc is required"},
		{"v2 get", "GET", "/api/shared/v2/ABCDEFGH?since=3", "", 500, "Database not connected"},
		{"v2 get validates since", "GET", "/api/shared/v2/ABCDEFGH?since=x", "", 400, "since must be an integer"},
		{"v2 sync", "POST", "/api/shared/v2/ABCDEFGH/sync", `{"since":0,"changes":{}}`, 500, "Database not connected"},
		{"v2 sync validates", "POST", "/api/shared/v2/ABCDEFGH/sync", `{"changes":{"records":{"":{}}}}`, 400, "entity id"},
		{"backup push", "POST", "/api/sync/push-uuid", `{"uuid":"u"}`, 500, "Database not connected"},
		{"backup pull", "GET", "/api/sync/pull-uuid/u", "", 500, "Database not connected"},
		{"ping", "GET", "/ping", "", 200, ""},
		{"unknown v2 path", "GET", "/api/shared/v2/ABCDEFGH/sync", "", 404, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%s %s = %d (%s), want %d", tc.method, tc.path, w.Code, w.Body.String(), tc.status)
			}
			if tc.errContains != "" && !strings.Contains(w.Body.String(), tc.errContains) {
				t.Errorf("body %s does not contain %q", w.Body.String(), tc.errContains)
			}
		})
	}

	// Body size limit on v2.
	big := `{"doc":{"book":{"r":{"note":{"v":"` + strings.Repeat("x", maxV2Body) + `","t":"1"}}}}}`
	req := httptest.NewRequest("POST", "/api/shared/v2", strings.NewReader(big))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "4 MB") {
		t.Errorf("oversized body = %d %s, want 400", w.Code, w.Body.String())
	}
}
