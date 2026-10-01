package app

import (
	"net/http"
	"strings"
)

// The visitor's original path, carried through the vercel.json rewrite. Vercel
// invokes the function at its own path (/api/index.go), not the one requested,
// so without it Gin matches no route and every URL — even /ping — answers
// Gin's "404 page not found".
//
//   - __vpath: the full path, interpolated into the rewrite's destination.
//   - vpath:   the rewrite's named parameter (the part after /api/), which
//     Vercel also passes through in the query string on its own — a fallback
//     in case the destination isn't interpolated.
const (
	vercelPathParam    = "__vpath"
	vercelSegmentParam = "vpath"
)

// isFunctionPath reports whether p is the function's own path rather than a
// route — the only case in which the vpath fallback is trusted.
func isFunctionPath(p string) bool {
	return p == "/api/index.go" || p == "/api/index" || p == "/api"
}

// RestoreRewrittenPath puts the original request path back on r before routing
// and drops the helper parameters so handlers see the client's query untouched.
// It is a no-op when neither is present (standalone main.go, tests).
func RestoreRewrittenPath(r *http.Request) {
	q := r.URL.Query()
	path := q.Get(vercelPathParam)
	if path == "" && isFunctionPath(r.URL.Path) && q.Has(vercelSegmentParam) {
		path = "/api/" + strings.TrimPrefix(q.Get(vercelSegmentParam), "/")
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return
	}
	q.Del(vercelPathParam)
	q.Del(vercelSegmentParam)
	r.URL.Path = path
	r.URL.RawPath = ""
	r.URL.RawQuery = q.Encode()
	r.RequestURI = r.URL.RequestURI()
}

// ServeHTTP is the entry for both deployments: it restores a rewritten path,
// then routes. It must wrap the router rather than be Gin middleware, because
// Gin matches the route before any middleware runs. Vercel may run either
// api/index.go (serverless handler) or main.go (as a Go server) — both go
// through here.
func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	RestoreRewrittenPath(r)
	GetRouter().ServeHTTP(w, r)
}
