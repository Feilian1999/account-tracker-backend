package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRestoreRewrittenPath(t *testing.T) {
	cases := []struct {
		target, wantPath, wantQuery string
	}{
		// What Vercel hands the function after the rewrite.
		{"/api/index.go?__vpath=/ping", "/ping", ""},
		{"/api/index.go?__vpath=/api/shared/share", "/api/shared/share", ""},
		// The client's own query survives, minus the helper parameter.
		{"/api/index.go?__vpath=/api/shared/v2/ABC&since=3", "/api/shared/v2/ABC", "since=3"},
		// Standalone server / no rewrite: untouched.
		{"/api/sync/pull-uuid/x?a=1", "/api/sync/pull-uuid/x", "a=1"},
		// Both carriers present (destination interpolated + named param passed through).
		{"/api/index.go?__vpath=/api/shared/share&vpath=shared/share", "/api/shared/share", ""},
		// Fallback: only Vercel's own pass-through of the named parameter.
		{"/api/index.go?vpath=shared/v2/ABC/sync&since=2", "/api/shared/v2/ABC/sync", "since=2"},
		{"/api/index.go?vpath=shared%2Fshare", "/api/shared/share", ""},
		// ...but never on a real route, where "vpath" would just be a client param.
		{"/api/shared/ABC?vpath=x", "/api/shared/ABC", "vpath=x"},
		// A non-absolute value is ignored and the request left as is (it then
		// 404s like any unknown path; only the rewrite sets the parameter).
		{"/api/index.go?__vpath=evil", "/api/index.go", "__vpath=evil"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, c.target, nil)
		RestoreRewrittenPath(r)
		if r.URL.Path != c.wantPath || r.URL.RawQuery != c.wantQuery {
			t.Errorf("%s → path %q query %q, want %q %q", c.target, r.URL.Path, r.URL.RawQuery, c.wantPath, c.wantQuery)
		}
	}
}

// The production failure end to end: a router that answered 404 for the
// function path now routes the restored one.
func TestRewrittenRequestReachesRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	before := httptest.NewRecorder()
	r.ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/api/index.go?__vpath=/ping", nil))
	if before.Code != http.StatusNotFound {
		t.Fatalf("without the fix the function path should 404, got %d", before.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/index.go?__vpath=/ping", nil)
	RestoreRewrittenPath(req)
	after := httptest.NewRecorder()
	r.ServeHTTP(after, req)
	if after.Code != http.StatusOK || after.Body.String() != "pong" {
		t.Fatalf("restored request should reach /ping, got %d %q", after.Code, after.Body.String())
	}
}

// Both entry points (main.go's server and api/index.go) use ServeHTTP; with no
// database it answers 500 "Database not connected" — i.e. it reached a route,
// where the production bug produced a 404 for the function path.
func TestServeHTTPRestoresBeforeRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/index.go?__vpath=%2Fapi%2Fshared%2FZZZZZZZZ&vpath=shared%2FZZZZZZZZ", nil))
	if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "/api/index.go") {
		t.Fatalf("still routed the function path: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/index.go?__vpath=%2Fping", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("/ping via rewrite: %d %s", rec.Code, rec.Body.String())
	}
}
