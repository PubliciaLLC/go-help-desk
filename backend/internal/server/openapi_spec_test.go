package server

// docs/api/openapi.yaml is hand-written. These tests are what keep it honest:
// they build the real router, walk it, and fail when the spec and the code
// disagree about which routes exist, which handler serves each one, or who may
// call it. When they disagree the code is right and the spec is what changes —
// unless the code is the bug, in which case the test has done its job anyway.
//
// No database: the router is built by New with nil services, which is enough
// because building it calls no service method. Nothing here serves a request
// through a handler; the auth probe runs each route's own middleware in front
// of a sentinel instead.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"go.yaml.in/yaml/v3"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// specFile is relative to this package's directory, which is where go test
// runs it.
var specFile = filepath.Join("..", "..", "..", "docs", "api", "openapi.yaml")

// specExcludedRoutes are routes the router serves that the spec deliberately
// does not describe, each with the reason. A stale entry fails the test.
var specExcludedRoutes = map[string]string{}

// specOnlyRoutes are operations the spec describes that this router does not
// serve because they are mounted elsewhere (cmd/server's ServeMux), each with
// the reason. A stale entry fails the test.
var specOnlyRoutes = map[string]string{}

var specMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// ── loading ────────────────────────────────────────────────────────────────

type specDoc struct {
	root map[string]any
	ops  map[string]specOp // "GET /api/v1/x"
}

type specOp struct {
	path, method string
	item, op     map[string]any
	line         int
}

func loadSpec(t *testing.T) specDoc {
	t.Helper()
	raw, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("reading the API spec: %v", err)
	}
	var root map[string]any
	// Unmarshal into a map refuses a duplicated key, which is the merge
	// mistake most likely to lose an operation silently.
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("%s does not parse: %v", specFile, err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		t.Fatalf("%s does not parse: %v", specFile, err)
	}
	lines := pathLines(&node)

	doc := specDoc{root: root, ops: map[string]specOp{}}
	paths, _ := root["paths"].(map[string]any)
	for p, v := range paths {
		item, _ := v.(map[string]any)
		for _, m := range specMethods {
			op, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			key := strings.ToUpper(m) + " " + p
			doc.ops[key] = specOp{path: p, method: strings.ToUpper(m), item: item, op: op, line: lines[p]}
		}
	}
	return doc
}

// pathLines maps each key under paths: to its line, for messages.
func pathLines(n *yaml.Node) map[string]int {
	out := map[string]int{}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "paths" {
			continue
		}
		ps := n.Content[i+1]
		for j := 0; j+1 < len(ps.Content); j += 2 {
			out[ps.Content[j].Value] = ps.Content[j].Line
		}
	}
	return out
}

// ── the router ─────────────────────────────────────────────────────────────

type walkedRoute struct {
	method, path, handler string
	mws                   []func(http.Handler) http.Handler
}

// probeSessionStore answers Get from the X-Probe-Session header, "role:mfa"
// or "role:nomfa", so the auth probe can present any session it likes.
type probeSessionStore struct{ SessionStore }

func (probeSessionStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	s := sessions.NewSession(nil, name)
	role, mfa, ok := strings.Cut(r.Header.Get("X-Probe-Session"), ":")
	if !ok {
		return s, nil // IsNew: no session
	}
	s.IsNew = false
	s.Values[auth.SessionDataKey] = auth.SessionData{UserID: uuid.New(), Role: user.Role(role), MFAPassed: mfa == "mfa"}
	return s, nil
}

type probeOAuthClients struct{}

func (probeOAuthClients) GetByClientID(_ context.Context, id string) (auth.OAuthClient, error) {
	if id != "probe" {
		return auth.OAuthClient{}, errors.New("unknown client")
	}
	return auth.OAuthClient{ClientID: id}, nil
}

type probeKey struct {
	role   user.Role
	scopes []string
}

const probeJWTSecret = "openapi-probe-secret"

// routeOnlyServer is New with no database. New dereferences exactly two of
// its collaborators while building: tickets (SetClosedReopenPolicy) and the
// OAuth client lookup (a method value). Everything else is captured, not
// called, until a request reaches it.
func routeOnlyServer(keys map[string]probeKey) *Server {
	lookup := authmw.APIKeyAuthFunc(func(_ context.Context, hashed string) (auth.APIKey, user.User, error) {
		k, ok := keys[hashed]
		if !ok {
			return auth.APIKey{}, user.User{}, errors.New("unknown key")
		}
		return auth.APIKey{Scopes: k.scopes}, user.User{ID: uuid.New(), Role: k.role}, nil
	})
	return New(&config.Config{JWTSecret: probeJWTSecret}, probeSessionStore{}, nil, &ticket.Service{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, lookup, probeOAuthClients{}, nil, nil, nil)
}

var chiParamRegexp = regexp.MustCompile(`\{([^}:]+):[^}]*\}`)

// canonicalPath is how a chi pattern is spelled in the spec: no trailing
// slash (a sub-router's "/" root answers both ways) and no regexp in a param.
func canonicalPath(p string) string {
	p = chiParamRegexp.ReplaceAllString(p, "{$1}")
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func funcName(f any) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}

var handlerName = regexp.MustCompile(`\.\(\*Server\)\.(\w+)-fm$`)

func walkRouter(t *testing.T, s *Server) map[string]walkedRoute {
	t.Helper()
	out := map[string]walkedRoute{}
	err := chi.Walk(s.router, func(method, route string, h http.Handler, mws ...func(http.Handler) http.Handler) error {
		p := canonicalPath(route)
		key := method + " " + p
		if _, dup := out[key]; dup {
			return fmt.Errorf("%s is registered twice once trailing slashes are ignored", key)
		}
		name := funcName(h)
		if m := handlerName.FindStringSubmatch(name); m != nil {
			name = m[1]
		}
		out[key] = walkedRoute{method: method, path: p, handler: name, mws: mws}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ── test 1: the route sets agree ───────────────────────────────────────────

// diffRoutes is the comparison, separate so its own behaviour is tested
// without editing the spec file (TestDiffRoutes).
func diffRoutes(router map[string]string, spec map[string]string, excluded, specOnly map[string]string) []string {
	var problems []string
	shape := func(k string) string { return regexp.MustCompile(`\{[^}]*\}`).ReplaceAllString(k, "{}") }
	near := func(k string, in map[string]string) string {
		var hits []string
		for o := range in {
			if o != k && (shape(o) == shape(k) || strings.SplitN(o, " ", 2)[1] == strings.SplitN(k, " ", 2)[1]) {
				hits = append(hits, o)
			}
		}
		sort.Strings(hits)
		if len(hits) == 0 {
			return ""
		}
		return " (closest: " + strings.Join(hits, ", ") + ")"
	}
	for k, handler := range router {
		_, inSpec := spec[k]
		_, isExcluded := excluded[k]
		switch {
		case inSpec && isExcluded:
			problems = append(problems, fmt.Sprintf("%s is in specExcludedRoutes but the spec documents it; remove the exclusion", k))
		case !inSpec && !isExcluded:
			problems = append(problems, fmt.Sprintf("%s is served by %s but the spec has no operation for it; document it, or add it to specExcludedRoutes with the reason%s", k, handler, near(k, spec)))
		case inSpec && spec[k] != handler:
			problems = append(problems, fmt.Sprintf("%s: x-ghd-handler is %q but the router serves it with %s", k, spec[k], handler))
		}
	}
	for k := range spec {
		_, inRouter := router[k]
		_, isSpecOnly := specOnly[k]
		switch {
		case inRouter && isSpecOnly:
			problems = append(problems, fmt.Sprintf("%s is in specOnlyRoutes but the router serves it; remove that entry", k))
		case !inRouter && !isSpecOnly:
			problems = append(problems, fmt.Sprintf("the spec documents %s, which the router does not serve; remove it or fix the method or path (path parameters use the chi names)%s", k, near(k, router)))
		}
	}
	for k := range excluded {
		if _, ok := router[k]; !ok {
			problems = append(problems, fmt.Sprintf("specExcludedRoutes lists %s, which the router no longer serves; remove the entry", k))
		}
	}
	for k := range specOnly {
		if _, ok := spec[k]; !ok {
			problems = append(problems, fmt.Sprintf("specOnlyRoutes lists %s, which the spec does not document; remove the entry", k))
		}
	}
	sort.Strings(problems)
	return problems
}

func TestOpenAPISpec_CoversEveryRoute(t *testing.T) {
	doc := loadSpec(t)
	routes := walkRouter(t, routeOnlyServer(nil))

	router := map[string]string{}
	for k, r := range routes {
		if strings.Contains(r.path, "*") {
			if _, ok := specExcludedRoutes[k]; !ok {
				t.Errorf("%s is a wildcard route; the spec cannot describe it as written, so add it to specExcludedRoutes with the reason", k)
			}
		}
		// cmd/server/main.go mounts this router at /api/ and /health and
		// nowhere else; a route outside them is documented but unreachable.
		if !strings.HasPrefix(r.path, "/api/") && r.path != "/health" {
			t.Errorf("%s is on the router but cmd/server mounts it only at /api/ and /health, so nothing can reach it", k)
		}
		router[k] = r.handler
	}
	spec := map[string]string{}
	for k, o := range doc.ops {
		h, _ := o.op["x-ghd-handler"].(string)
		spec[k] = h
	}
	for _, p := range diffRoutes(router, spec, specExcludedRoutes, specOnlyRoutes) {
		t.Errorf("%s: %s", specFile, p)
	}
}

func TestDiffRoutes(t *testing.T) {
	router := map[string]string{
		"GET /api/v1/a":                    "handleA",
		"POST /api/v1/a":                   "handleCreateA",
		"GET /api/v1/b/{id}/c/{typeId}":    "handleC",
		"DELETE /api/v1/x/{id}":            "handleX",
		"GET /api/v1/excluded":             "handleEx",
		"PATCH /api/v1/served-not-in-spec": "handleS",
	}
	cases := []struct {
		name     string
		mutate   func(spec map[string]string)
		excluded map[string]string
		specOnly map[string]string
		want     []string // substrings, one per expected problem
	}{
		{name: "in agreement", want: nil},
		{name: "operation dropped from spec", mutate: func(s map[string]string) { delete(s, "POST /api/v1/a") },
			want: []string{"POST /api/v1/a is served by handleCreateA"}},
		{name: "dead path in spec", mutate: func(s map[string]string) { s["GET /api/v1/gone"] = "handleGone" },
			want: []string{"documents GET /api/v1/gone"}},
		{name: "wrong method", mutate: func(s map[string]string) { delete(s, "DELETE /api/v1/x/{id}"); s["PUT /api/v1/x/{id}"] = "handleX" },
			want: []string{"documents PUT /api/v1/x/{id}", "DELETE /api/v1/x/{id} is served"}},
		{name: "renamed path parameter", mutate: func(s map[string]string) {
			delete(s, "GET /api/v1/b/{id}/c/{typeId}")
			s["GET /api/v1/b/{id}/c/{typeID}"] = "handleC"
		}, want: []string{"closest: GET /api/v1/b/{id}/c/{typeId}", "closest: GET /api/v1/b/{id}/c/{typeID}"}},
		{name: "wrong handler", mutate: func(s map[string]string) { s["GET /api/v1/a"] = "handleOther" },
			want: []string{`x-ghd-handler is "handleOther"`}},
		{name: "stale exclusion", excluded: map[string]string{"GET /api/v1/excluded": "x", "GET /api/v1/never": "y"},
			want: []string{"specExcludedRoutes lists GET /api/v1/never"}},
		{name: "excluded yet documented", mutate: func(s map[string]string) { s["GET /api/v1/excluded"] = "handleEx" },
			excluded: map[string]string{"GET /api/v1/excluded": "x"},
			want:     []string{"in specExcludedRoutes but the spec documents it"}},
		{name: "stale spec-only entry", specOnly: map[string]string{"GET /mcp/sse": "mounted on the ServeMux"},
			want: []string{"specOnlyRoutes lists GET /mcp/sse"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := map[string]string{}
			for k, v := range router {
				spec[k] = v
			}
			excluded := tc.excluded
			if excluded == nil {
				excluded = map[string]string{}
			}
			if _, ok := excluded["GET /api/v1/excluded"]; ok {
				delete(spec, "GET /api/v1/excluded")
			}
			if tc.mutate != nil {
				tc.mutate(spec)
			}
			specOnly := tc.specOnly
			if specOnly == nil {
				specOnly = map[string]string{}
			}
			got := diffRoutes(router, spec, excluded, specOnly)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d problems, want %d:\n%s", len(got), len(tc.want), strings.Join(got, "\n"))
			}
			for _, w := range tc.want {
				if !slices.ContainsFunc(got, func(g string) bool { return strings.Contains(g, w) }) {
					t.Errorf("no problem mentions %q:\n%s", w, strings.Join(got, "\n"))
				}
			}
		})
	}
}

// ── test 2: the document is well formed ────────────────────────────────────

var (
	opIDPattern    = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	pathPattern    = regexp.MustCompile(`^/(?:[a-z0-9-]+|\{[A-Za-z][A-Za-z0-9]*\})(?:/(?:[a-z0-9-]+|\{[A-Za-z][A-Za-z0-9]*\}))*$`)
	statusPattern  = regexp.MustCompile(`^[1-5][0-9][0-9]$`)
	templateParams = regexp.MustCompile(`\{([^}]+)\}`)
	pathItemKeys   = []string{"summary", "description", "parameters"}
)

func TestOpenAPISpec_IsWellFormed(t *testing.T) {
	doc := loadSpec(t)
	root := doc.root
	fail := func(format string, args ...any) { t.Errorf("%s: "+format, append([]any{specFile}, args...)...) }

	if v, _ := root["openapi"].(string); !strings.HasPrefix(v, "3.1.") {
		fail("openapi is %q, want 3.1.x", root["openapi"])
	}
	info, _ := root["info"].(map[string]any)
	if s, _ := info["title"].(string); s == "" {
		fail("info.title is empty")
	}
	if s, _ := info["version"].(string); s == "" {
		fail("info.version is empty")
	}
	if _, ok := root["security"]; ok {
		fail("top-level security is not used; every operation states its own")
	}

	// Every $ref is local and resolves; remember what was referenced.
	referenced := map[string]bool{}
	var walk func(v any, at string)
	walk = func(v any, at string) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				if !strings.HasPrefix(ref, "#/") {
					fail("%s: $ref %q is not local", at, ref)
				} else if _, ok := resolvePointer(root, ref); !ok {
					fail("%s: $ref %q does not resolve", at, ref)
				} else {
					referenced[ref] = true
				}
			}
			for k, c := range x {
				walk(c, at+"/"+k)
			}
		case []any:
			for i, c := range x {
				walk(c, fmt.Sprintf("%s/%d", at, i))
			}
		}
	}
	walk(root, "#")

	// Nothing defined and never used, so merged fragments leave no litter.
	comps, _ := root["components"].(map[string]any)
	for kind, v := range comps {
		if kind == "securitySchemes" {
			continue
		}
		defs, _ := v.(map[string]any)
		for name := range defs {
			if !referenced["#/components/"+kind+"/"+name] {
				fail("components.%s.%s is never referenced", kind, name)
			}
		}
	}

	schemes, _ := comps["securitySchemes"].(map[string]any)
	declaredTags := map[string]bool{}
	tags, _ := root["tags"].([]any)
	for _, tg := range tags {
		m, _ := tg.(map[string]any)
		name, _ := m["name"].(string)
		if declaredTags[name] {
			fail("tag %q is declared twice", name)
		}
		declaredTags[name] = true
	}
	usedTags := map[string]bool{}
	opIDs := map[string]string{}

	paths, _ := root["paths"].(map[string]any)
	for p, v := range paths {
		if !pathPattern.MatchString(p) {
			fail("path %q is not canonical (lowercase segments, {param} names, no trailing slash)", p)
		}
		item, _ := v.(map[string]any)
		for k := range item {
			if !slices.Contains(specMethods, k) && !slices.Contains(pathItemKeys, k) {
				fail("%s: path item key %q is not allowed (no $ref, servers or extensions on path items)", p, k)
			}
		}
	}

	for key, o := range doc.ops {
		where := fmt.Sprintf("line %d %s", o.line, key)
		id, _ := o.op["operationId"].(string)
		switch {
		case !opIDPattern.MatchString(id):
			fail("%s: operationId %q is missing or not lowerCamelCase", where, id)
		case opIDs[id] != "":
			fail("%s: operationId %q is also used by %s", where, id, opIDs[id])
		default:
			opIDs[id] = key
		}
		if s, _ := o.op["summary"].(string); strings.TrimSpace(s) == "" || strings.HasPrefix(s, "TODO") {
			fail("%s: summary is missing", where)
		}
		ts, _ := o.op["tags"].([]any)
		if len(ts) != 1 {
			fail("%s: wants exactly one tag, has %d", where, len(ts))
		}
		for _, tg := range ts {
			name, _ := tg.(string)
			if !declaredTags[name] {
				fail("%s: tag %q is not declared in tags", where, name)
			}
			usedTags[name] = true
		}
		if h, _ := o.op["x-ghd-handler"].(string); h == "" {
			fail("%s: x-ghd-handler is missing", where)
		}
		sec, ok := o.op["security"].([]any)
		if !ok {
			fail("%s: security must be stated, [] for a public operation", where)
		}
		for _, req := range sec {
			for name := range req.(map[string]any) {
				if _, ok := schemes[name]; !ok {
					fail("%s: security scheme %q is not declared", where, name)
				}
			}
		}
		resps, _ := o.op["responses"].(map[string]any)
		if len(resps) == 0 {
			fail("%s: no responses", where)
		}
		hasSuccess := false
		for code, r := range resps {
			if !statusPattern.MatchString(code) {
				fail("%s: response %q is not a status code (no default, no ranges)", where, code)
			}
			if code < "400" {
				hasSuccess = true
			}
			rm, _ := r.(map[string]any)
			if _, isRef := rm["$ref"]; !isRef {
				if d, _ := rm["description"].(string); strings.TrimSpace(d) == "" || strings.HasPrefix(d, "TODO") {
					fail("%s: response %s has no description", where, code)
				}
			}
		}
		// A stub (handler answers 501 not_implemented and nothing else) says
		// so with x-ghd-stub, and is the only operation allowed no success.
		if stub, _ := o.op["x-ghd-stub"].(bool); stub {
			if hasSuccess || resps["501"] == nil {
				fail("%s: x-ghd-stub operations document 501 and no success response", where)
			}
		} else if !hasSuccess {
			fail("%s: no 1xx/2xx/3xx response (a 501 stub says x-ghd-stub: true)", where)
		}

		// Template parameters and declared path parameters are the same set,
		// each declared once and required.
		want := map[string]bool{}
		for _, m := range templateParams.FindAllStringSubmatch(o.path, -1) {
			want[m[1]] = true
		}
		got := map[string]int{}
		for _, list := range []any{o.item["parameters"], o.op["parameters"]} {
			ps, _ := list.([]any)
			for _, pv := range ps {
				pm, _ := pv.(map[string]any)
				if ref, ok := pm["$ref"].(string); ok {
					r, _ := resolvePointer(root, ref)
					pm, _ = r.(map[string]any)
				}
				if pm["in"] != "path" {
					continue
				}
				name, _ := pm["name"].(string)
				got[name]++
				if pm["required"] != true {
					fail("%s: path parameter %q must be required: true", where, name)
				}
			}
		}
		for n := range want {
			if got[n] == 0 {
				fail("%s: path parameter {%s} is not declared", where, n)
			}
		}
		for n, c := range got {
			if !want[n] {
				fail("%s: declares path parameter %q that is not in the path", where, n)
			}
			if c > 1 {
				fail("%s: path parameter %q is declared %d times", where, n, c)
			}
		}
	}
	for name := range declaredTags {
		if !usedTags[name] {
			fail("tag %q is declared but no operation uses it", name)
		}
	}
}

func resolvePointer(root any, ref string) (any, bool) {
	cur := root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// ── test 3: who may call each route ────────────────────────────────────────

// routeAuth is what the router's own middleware admits, measured.
type routeAuth struct {
	public, guest, anonymousWhenGuests, ticketAccess bool
	roles                                            []string
	mfa, apiKey, oauth                               bool
	scope                                            string
}

// Middleware the probe does not need to run: not authentication.
var probeIgnored = []string{
	"chi/v5/middleware.RequestID", "chi/v5/middleware.Recoverer",
	"server.requestLogger", "server.securityHeaders", "server.reputationDeadline",
}

func probeRoute(t *testing.T, r walkedRoute, keys map[string]probeKey) routeAuth {
	t.Helper()
	var ra routeAuth
	var chain []func(http.Handler) http.Handler
	for _, m := range r.mws {
		name := funcName(m)
		switch {
		case slices.ContainsFunc(probeIgnored, func(s string) bool { return strings.Contains(name, s) }):
		// Recognised by name and not run: each reaches a service the
		// route-only server does not have.
		case strings.Contains(name, ".requireTicketAccess"):
			ra.ticketAccess = true
		case strings.Contains(name, ".requireSignedInOrGuestsEnabled"):
			ra.anonymousWhenGuests = true
		case strings.Contains(name, "internal/middleware.GuestAuth"):
			ra.guest = true
		case strings.Contains(name, "internal/middleware."):
			chain = append(chain, m)
		default:
			t.Fatalf("%s %s: the probe does not know middleware %s; teach probeRoute what it means for authentication", r.method, r.path, name)
		}
	}
	reaches := func(header, value string) bool {
		reached := false
		var h http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
		for i := len(chain) - 1; i >= 0; i-- {
			h = chain[i](h)
		}
		req := httptest.NewRequest(r.method, "/", nil)
		if header != "" {
			req.Header.Set(header, value)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		return reached
	}
	n := len(keys)
	apiKey := func(role user.Role, scopes []string) string {
		n++
		raw := fmt.Sprintf("probe-key-%d", n)
		keys[auth.HashToken(raw)] = probeKey{role, scopes}
		return "ApiKey " + raw
	}
	var all []string
	for _, sc := range auth.All() {
		all = append(all, sc.String())
	}
	if ra.guest {
		return ra
	}
	ra.public = !ra.anonymousWhenGuests && reaches("", "")
	if ra.public {
		return ra
	}
	var owner user.Role
	for _, role := range []user.Role{user.RoleAdmin, user.RoleStaff, user.RoleUser} {
		if reaches("X-Probe-Session", string(role)+":mfa") {
			ra.roles = append(ra.roles, string(role))
			if owner == "" {
				owner = role
			}
			if !reaches("X-Probe-Session", string(role)+":nomfa") {
				ra.mfa = true
			}
		}
	}
	if owner == "" {
		return ra
	}
	ra.apiKey = reaches("Authorization", apiKey(owner, all))
	if ra.apiKey && !reaches("Authorization", apiKey(owner, nil)) {
		for _, sc := range all { // read before write, so a GET finds :read
			if reaches("Authorization", apiKey(owner, []string{sc})) {
				ra.scope = sc
				break
			}
		}
	}
	tok, err := auth.IssueAccessToken(auth.OAuthClient{ID: uuid.New(), ClientID: "probe", Scopes: all}, probeJWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	ra.oauth = reaches("Authorization", "Bearer "+tok)
	return ra
}

func TestOpenAPISpec_AuthMatchesRouter(t *testing.T) {
	doc := loadSpec(t)
	keys := map[string]probeKey{}
	routes := walkRouter(t, routeOnlyServer(keys))

	var problems []string
	for key, r := range routes {
		o, ok := doc.ops[key]
		if !ok {
			continue // TestOpenAPISpec_CoversEveryRoute reports it
		}
		ra := probeRoute(t, r, keys)
		bad := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("line %d %s: ", o.line, key)+fmt.Sprintf(format, args...))
		}
		got := securityOf(o.op)
		want := expectedSecurity(ra)
		if !slices.Equal(got, want) {
			bad("security is %v; the router admits %v", got, want)
		}
		gotRoles := stringList(o.op["x-ghd-roles"])
		if !slices.Equal(gotRoles, ra.roles) {
			bad("x-ghd-roles is %v; the router admits sessions with roles %v", gotRoles, ra.roles)
		}
		gotMFA, _ := o.op["x-ghd-mfa"].(bool)
		if _, present := o.op["x-ghd-mfa"]; present != (len(ra.roles) > 0) || gotMFA != ra.mfa {
			bad("x-ghd-mfa is %v; the router requires a passed second factor: %v", o.op["x-ghd-mfa"], ra.mfa)
		}
		when, _ := o.op["x-ghd-anonymous-when"].(string)
		if (when == "guest_submission_enabled") != ra.anonymousWhenGuests || (when != "" && !ra.anonymousWhenGuests) {
			bad("x-ghd-anonymous-when is %q; the router admits anonymous callers when guests are enabled: %v", when, ra.anonymousWhenGuests)
		}
		resps, _ := o.op["responses"].(map[string]any)
		has := func(code string) bool { _, ok := resps[code]; return ok }
		refIs := func(code, name string) bool {
			m, _ := resps[code].(map[string]any)
			return m["$ref"] == "#/components/responses/"+name
		}
		gated := !ra.public && !ra.guest
		if gated && !has("401") {
			bad("an authenticated operation must document 401")
		}
		restricted := len(ra.roles) < 3 || ra.mfa || ra.scope != "" || (gated && !ra.apiKey)
		if gated && restricted && !has("403") {
			bad("the router can refuse this with 403 (role, MFA, scope or machine credential); document it")
		}
		if ra.ticketAccess && !refIs("404", "TicketNotFound") {
			bad("routes under /tickets/{id} answer 404 for a ticket the caller may not see; 404 must be $ref TicketNotFound")
		}
		if ra.guest && !refIs("404", "GuestNotFound") {
			bad("guest-token routes refuse with the single guest 404; 404 must be $ref GuestNotFound")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("%s: %s", specFile, p)
	}
}

// securityOf renders an operation's security requirements as sorted strings
// such as "apiKey:tickets:read", "sessionCookie", "anonymous".
func securityOf(op map[string]any) []string {
	var out []string
	reqs, _ := op["security"].([]any)
	for _, req := range reqs {
		m, _ := req.(map[string]any)
		if len(m) == 0 {
			out = append(out, "anonymous")
		}
		for name, v := range m {
			s := name
			if vals := stringList(v); len(vals) > 0 {
				s += ":" + strings.Join(vals, ",")
			}
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func expectedSecurity(ra routeAuth) []string {
	var out []string
	switch {
	case ra.public:
		return nil
	case ra.guest:
		return []string{"guestToken"}
	}
	if ra.anonymousWhenGuests {
		out = append(out, "anonymous")
	}
	if len(ra.roles) > 0 {
		out = append(out, "sessionCookie")
	}
	suffix := ""
	if ra.scope != "" {
		suffix = ":" + ra.scope
	}
	if ra.apiKey {
		out = append(out, "apiKey"+suffix)
	}
	if ra.oauth {
		out = append(out, "oauthClient"+suffix)
	}
	sort.Strings(out)
	return out
}

func stringList(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
