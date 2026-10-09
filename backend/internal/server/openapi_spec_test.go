package server

// docs/api/openapi.yaml is hand-written. These tests are what keep it honest:
// they build the real router, walk it, and fail when the spec and the code
// disagree about which routes exist, which handler serves each one, or who may
// call it. When they disagree the code is right and the spec is what changes,
// unless the code is the bug, in which case the test has done its job anyway.
//
// No database: the router is built by New with nil for almost every service.
// Building it touches only the two collaborators routeOnlyServer supplies, and
// nothing here serves a request through a handler: the auth probe runs each
// route's own middleware in front of a sentinel instead.

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
	"github.com/publiciallc/go-help-desk/backend/internal/version"
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

// specRoles is every session role, in the order x-ghd-roles lists them.
var specRoles = []string{string(user.RoleAdmin), string(user.RoleStaff), string(user.RoleUser)}

// The Authorization prefixes the security schemes document. The probe sends
// the first two, so a wrong one fails test 3; the guest one is checked by
// TestOpenAPISpec_FrameMatchesCode.
const (
	apiKeyPrefix = "ApiKey "
	bearerPrefix = "Bearer "
	guestPrefix  = "Guest "
)

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

// String names the operation in a failure message: where it is, what it
// is, and its operationId.
func (o specOp) String() string {
	id, _ := o.op["operationId"].(string)
	return fmt.Sprintf("line %d %s %s (%s)", o.line, o.method, o.path, id)
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
			line := lines[key]
			if line == 0 {
				line = lines[p]
			}
			doc.ops[key] = specOp{path: p, method: strings.ToUpper(m), item: item, op: op, line: line}
		}
	}
	return doc
}

// pathLines maps each key under paths: to its line, and each operation,
// "GET /api/v1/x", to the line of its method key, for messages.
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
			p := ps.Content[j].Value
			out[p] = ps.Content[j].Line
			item := ps.Content[j+1]
			for k := 0; k+1 < len(item.Content); k += 2 {
				out[strings.ToUpper(item.Content[k].Value)+" "+p] = item.Content[k].Line
			}
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

// anyParam reduces a route key to its shape, so a renamed parameter can be
// suggested as the closest match.
var anyParam = regexp.MustCompile(`\{[^}]*\}`)

// diffRoutes is the comparison, separate so its own behaviour is tested
// without editing the spec file (TestDiffRoutes).
func diffRoutes(router map[string]string, spec map[string]string, excluded, specOnly map[string]string) []string {
	var problems []string
	shape := func(k string) string { return anyParam.ReplaceAllString(k, "{}") }
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
		{name: "spec-only yet served", specOnly: map[string]string{"GET /api/v1/a": "mounted on the ServeMux"},
			want: []string{"GET /api/v1/a is in specOnlyRoutes but the router serves it"}},
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

const (
	responsesPrefix = "#/components/responses/"
	ticketRefParam  = "#/components/parameters/TicketRef"
)

// refsIn lists every $ref under v.
func refsIn(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok {
			out = append(out, ref)
		}
		for _, c := range x {
			out = append(out, refsIn(c)...)
		}
	case []any:
		for _, c := range x {
			out = append(out, refsIn(c)...)
		}
	}
	return out
}

// reachableRefs is every $ref reachable from outside components, following
// each one into the component it names. A component that only refers to
// itself, or to another unused one, is not reachable.
func reachableRefs(root map[string]any) map[string]bool {
	var queue []string
	for k, v := range root {
		if k != "components" {
			queue = append(queue, refsIn(v)...)
		}
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if target, ok := resolvePointer(root, ref); ok {
			queue = append(queue, refsIn(target)...)
		}
	}
	return seen
}

// callerSentence is the first sentence of a role-restricted operation's
// description, generated from what the router measurably admits (the roles,
// whether a second factor is required, and whether a machine credential gets
// in), so the prose cannot drift from the code.
func callerSentence(roles []string, mfa, machine bool) string {
	noun := map[string]string{"admin": "administrators", "staff": "staff", "user": "reporting users"}
	var who []string
	for i := len(roles) - 1; i >= 0; i-- {
		w, ok := noun[roles[i]]
		if !ok {
			w = fmt.Sprintf("%q", roles[i]) // not a role; the caller reports it
		}
		who = append(who, w)
	}
	if len(who) == 0 {
		return ""
	}
	s := who[len(who)-1]
	if len(who) > 1 {
		s = strings.Join(who[:len(who)-1], ", ") + " and " + s
	}
	s = strings.ToUpper(s[:1]) + s[1:] + " only"
	if !machine {
		s += ", in a signed-in session"
	}
	if mfa {
		s += " (second factor required)"
	}
	return s + "."
}

func TestCallerSentence(t *testing.T) {
	cases := []struct {
		roles        []string
		mfa, machine bool
		want         string
	}{
		{[]string{"admin"}, true, true, "Administrators only (second factor required)."},
		{[]string{"admin", "staff"}, true, true, "Staff and administrators only (second factor required)."},
		{[]string{"admin"}, true, false, "Administrators only, in a signed-in session (second factor required)."},
		{[]string{"admin", "user"}, false, true, "Reporting users and administrators only."},
		// Not a role: no panic, and nothing a real description would match.
		{[]string{"admins"}, true, true, `"admins" only (second factor required).`},
		{nil, true, true, ""},
	}
	for _, tc := range cases {
		if got := callerSentence(tc.roles, tc.mfa, tc.machine); got != tc.want {
			t.Errorf("callerSentence(%v, %v, %v) = %q, want %q", tc.roles, tc.mfa, tc.machine, got, tc.want)
		}
	}
}

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

	paths, _ := root["paths"].(map[string]any)

	// Every $ref is local and resolves, and every key is a string.
	var walk func(v any, at string)
	walk = func(v any, at string) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				if !strings.HasPrefix(ref, "#/") {
					fail("%s: $ref %q is not local", at, ref)
				} else if _, ok := resolvePointer(root, ref); !ok {
					fail("%s: $ref %q does not resolve", at, ref)
				}
				// Beside a Reference Object (anything but a schema) OpenAPI
				// 3.1 ignores every key except summary and description.
				if !strings.HasPrefix(ref, "#/components/schemas/") {
					for k := range x {
						if k != "$ref" && k != "summary" && k != "description" {
							fail("%s: %q beside $ref %q is ignored by OpenAPI 3.1", at, k, ref)
						}
					}
				}
			}
			if _, ok := x["nullable"].(bool); ok {
				fail("%s: nullable is OpenAPI 3.0; in 3.1 write type: [<type>, 'null']", at)
			}
			for k, c := range x {
				walk(c, at+"/"+k)
			}
		case map[any]any:
			for k, c := range x {
				if _, ok := k.(string); !ok {
					fail("%s: key %v is a %T, not a string; quote it (a status code is written '200')", at, k, k)
				}
				walk(c, fmt.Sprintf("%s/%v", at, k))
			}
		case []any:
			for i, c := range x {
				walk(c, fmt.Sprintf("%s/%d", at, i))
			}
		}
	}
	for k, v := range root {
		if k != "paths" {
			walk(v, "#/"+k)
		}
	}
	for p, v := range paths {
		item, _ := v.(map[string]any)
		for k, c := range item {
			if o, ok := doc.ops[strings.ToUpper(k)+" "+p]; ok {
				walk(c, o.String())
			} else {
				walk(c, p+" "+k)
			}
		}
	}

	// Nothing defined and never used, so merged fragments leave no litter.
	reachable := reachableRefs(root)
	comps, _ := root["components"].(map[string]any)
	for kind, v := range comps {
		if kind == "securitySchemes" {
			continue // used by name, not by $ref: checked below
		}
		defs, _ := v.(map[string]any)
		for name := range defs {
			if !reachable["#/components/"+kind+"/"+name] {
				fail("components.%s.%s is never referenced from an operation", kind, name)
			}
		}
	}

	schemes, _ := comps["securitySchemes"].(map[string]any)
	usedSchemes := map[string]bool{}
	declaredTags := map[string]bool{}
	tags, _ := root["tags"].([]any)
	for _, tg := range tags {
		m, _ := tg.(map[string]any)
		name, _ := m["name"].(string)
		if declaredTags[name] {
			fail("tag %q is declared twice", name)
		}
		declaredTags[name] = true
		if d, _ := m["description"].(string); strings.TrimSpace(d) == "" {
			fail("tag %q has no description", name)
		}
	}
	usedTags := map[string]bool{}
	opIDs := map[string]string{}

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
		where := o.String()
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
		} else if !strings.HasSuffix(s, ".") {
			fail("%s: summary %q does not end with a period, as every other summary does", where, s)
		}
		for _, role := range stringList(o.op["x-ghd-roles"]) {
			if !slices.Contains(specRoles, role) {
				fail("%s: x-ghd-roles has %q, which is not a role (%s)", where, role, strings.Join(specRoles, ", "))
			}
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
		for i, req := range sec {
			m, ok := req.(map[string]any)
			if !ok {
				fail("%s: security[%d] is %v, want a map from scheme to scopes such as {sessionCookie: []}", where, i, req)
				continue
			}
			if len(m) > 1 {
				fail("%s: security[%d] names %d schemes in one requirement, which means a caller needs all of them at once; the router never requires two credentials together, so give each its own entry", where, i, len(m))
			}
			for name, scopes := range m {
				if _, ok := schemes[name]; !ok {
					fail("%s: security scheme %q is not declared", where, name)
				}
				if _, ok := scopes.([]any); !ok {
					fail("%s: security scheme %q has %v for scopes, want a list", where, name, scopes)
				}
				usedSchemes[name] = true
			}
		}

		rv, present := o.op["responses"]
		resps, isMap := rv.(map[string]any)
		if !present || (isMap && len(resps) == 0) {
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
			if ref, isRef := rm["$ref"].(string); isRef {
				if !strings.HasPrefix(ref, responsesPrefix) {
					fail("%s: response %s is $ref %q; a response refers to %s", where, code, ref, responsesPrefix)
				}
				if code == "401" && ref != responsesPrefix+"Unauthorized" {
					fail("%s: response 401 is $ref %q; a 401 that refers to a component refers to Unauthorized", where, ref)
				}
			} else if d, _ := rm["description"].(string); strings.TrimSpace(d) == "" || strings.HasPrefix(d, "TODO") {
				fail("%s: response %s has no description", where, code)
			}
		}
		// A stub (handler answers 501 not_implemented and nothing else) says
		// so with x-ghd-stub, and is the only operation allowed no success.
		if stub, _ := o.op["x-ghd-stub"].(bool); stub {
			if hasSuccess || resps["501"] == nil {
				fail("%s: x-ghd-stub operations document 501 and no success response", where)
			}
		} else if isMap && !hasSuccess {
			fail("%s: no 1xx/2xx/3xx response (a 501 stub says x-ghd-stub: true)", where)
		}

		// Each parameter is declared once, and template parameters and
		// declared path parameters are the same set, each required.
		want := map[string]bool{}
		for _, m := range templateParams.FindAllStringSubmatch(o.path, -1) {
			want[m[1]] = true
		}
		got := map[string]bool{}
		declared := map[string]int{}
		for _, pm := range operationParams(root, o) {
			name, _ := pm["name"].(string)
			in, _ := pm["in"].(string)
			declared[in+" "+name]++
			if in != "path" {
				continue
			}
			got[name] = true
			if pm["required"] != true {
				fail("%s: path parameter %q must be required: true", where, name)
			}
		}
		for k, c := range declared {
			if c > 1 {
				fail("%s: parameter %s is declared %d times", where, k, c)
			}
		}
		for n := range want {
			if !got[n] {
				fail("%s: path parameter {%s} is not declared", where, n)
			}
		}
		for n := range got {
			if !want[n] {
				fail("%s: declares path parameter %q that is not in the path", where, n)
			}
		}
	}
	for name := range declaredTags {
		if !usedTags[name] {
			fail("tag %q is declared but no operation uses it", name)
		}
	}
	for name := range schemes {
		if !usedSchemes[name] {
			fail("components.securitySchemes.%s is never used by an operation", name)
		}
	}
}

// operationParams is every parameter that applies to an operation, path
// item's and operation's own, with $refs resolved.
func operationParams(root map[string]any, o specOp) []map[string]any {
	var out []map[string]any
	for _, list := range []any{o.item["parameters"], o.op["parameters"]} {
		ps, _ := list.([]any)
		for _, pv := range ps {
			pm, _ := pv.(map[string]any)
			if ref, ok := pm["$ref"].(string); ok {
				r, _ := resolvePointer(root, ref)
				pm, _ = r.(map[string]any)
			}
			if pm != nil {
				out = append(out, pm)
			}
		}
	}
	return out
}

// declaresParamRef reports whether the operation takes the parameter as a
// $ref to target, at the path item or on the operation.
func declaresParamRef(o specOp, target string) bool {
	for _, list := range []any{o.item["parameters"], o.op["parameters"]} {
		ps, _ := list.([]any)
		for _, pv := range ps {
			if pm, _ := pv.(map[string]any); pm["$ref"] == target {
				return true
			}
		}
	}
	return false
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

// ── the frame: what the shared components say about the code ──────────────

func TestOpenAPISpec_FrameMatchesCode(t *testing.T) {
	doc := loadSpec(t)
	info, _ := doc.root["info"].(map[string]any)
	if v, _ := info["version"].(string); v != version.Version {
		t.Errorf("%s: info.version is %q, but the server reports %q", specFile, v, version.Version)
	}

	comps, _ := doc.root["components"].(map[string]any)
	schemes, _ := comps["securitySchemes"].(map[string]any)
	cases := []struct {
		name     string
		fields   map[string]string
		mentions string // the description shows the caller this
	}{
		{"sessionCookie", map[string]string{"type": "apiKey", "in": "cookie", "name": auth.SessionName}, ""},
		{"apiKey", map[string]string{"type": "apiKey", "in": "header", "name": "Authorization"}, "Authorization: " + apiKeyPrefix},
		{"oauthClient", map[string]string{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}, "Authorization: " + bearerPrefix},
		{"guestToken", map[string]string{"type": "apiKey", "in": "header", "name": "Authorization"}, "Authorization: " + guestPrefix},
	}
	if len(schemes) != len(cases) {
		t.Errorf("%s: %d security schemes are declared, want the %d the auth middleware implements", specFile, len(schemes), len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := schemes[tc.name].(map[string]any)
			if !ok {
				t.Fatalf("%s: components.securitySchemes.%s is missing", specFile, tc.name)
			}
			for k, want := range tc.fields {
				if got, _ := s[k].(string); got != want {
					t.Errorf("%s: securitySchemes.%s.%s is %q, want %q", specFile, tc.name, k, got, want)
				}
			}
			if d, _ := s["description"].(string); !strings.Contains(d, tc.mentions) {
				t.Errorf("%s: securitySchemes.%s.description does not show %q", specFile, tc.name, tc.mentions)
			}
		})
	}

	// The guest prefix is the one the auth probe never sends.
	reached := false
	h := authmw.GuestAuth(func(context.Context, string) (string, error) { return "ticket", nil })(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", guestPrefix+"token")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !reached {
		t.Errorf("GuestAuth refuses %q, which securitySchemes.guestToken documents", guestPrefix+"<token>")
	}
}

// ── test 3: who may call each route ────────────────────────────────────────

// routeAuth is what the router's own middleware admits, measured.
type routeAuth struct {
	public, guest, anonymousWhenGuests, ticketAccess bool
	roles                                            []string
	mfa                                              bool
	apiKey, oauth                                    bool
	keyScopes, oauthScopes                           []string // a smallest set that is admitted
	anonStatus                                       int      // what a caller with no credential got
	refusals                                         map[int]bool
}

// Middleware the probe does not need to run: not authentication.
var probeIgnored = []string{
	"chi/v5/middleware.RequestID", "chi/v5/middleware.Recoverer",
	"server.requestLogger", "server.securityHeaders", "server.reputationDeadline",
}

// minimalScopes shrinks all to a smallest set that admits still accepts,
// dropping later scopes first: auth.All lists read before write, so write,
// which implies read, goes before the read it would otherwise shadow.
func minimalScopes(all []string, admits func([]string) bool) []string {
	cur := slices.Clone(all)
	for i := len(all) - 1; i >= 0; i-- {
		drop := all[i]
		trial := slices.DeleteFunc(slices.Clone(cur), func(s string) bool { return s == drop })
		if admits(trial) {
			cur = trial
		}
	}
	sort.Strings(cur)
	return cur
}

func probeRoute(t *testing.T, r walkedRoute, keys map[string]probeKey) routeAuth {
	t.Helper()
	ra := routeAuth{refusals: map[int]bool{}}
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
	if ra.guest {
		return ra
	}
	// reach runs the chain and reports whether the sentinel was reached and,
	// when it was not, the status the router answered with instead.
	reach := func(header, value string) (bool, int) {
		reached := false
		var h http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
		for i := len(chain) - 1; i >= 0; i-- {
			h = chain[i](h)
		}
		req := httptest.NewRequest(r.method, "/", nil)
		if header != "" {
			req.Header.Set(header, value)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return reached, rec.Code
	}
	admits := func(header, value string) bool {
		ok, code := reach(header, value)
		if !ok {
			ra.refusals[code] = true
		}
		return ok
	}
	n := len(keys)
	apiKey := func(role user.Role, scopes []string) string {
		n++
		raw := fmt.Sprintf("probe-key-%d", n)
		keys[auth.HashToken(raw)] = probeKey{role, scopes}
		return apiKeyPrefix + raw
	}
	bearer := func(scopes []string) string {
		tok, err := auth.IssueAccessToken(auth.OAuthClient{ID: uuid.New(), ClientID: "probe", Scopes: scopes}, probeJWTSecret)
		if err != nil {
			t.Fatal(err)
		}
		return bearerPrefix + tok
	}
	var all []string
	for _, sc := range auth.All() {
		all = append(all, sc.String())
	}

	if ok, code := reach("", ""); !ok {
		ra.anonStatus = code
		ra.refusals[code] = true
	} else if !ra.anonymousWhenGuests {
		ra.public = true
		return ra
	}
	var owner user.Role
	for _, role := range []user.Role{user.RoleAdmin, user.RoleStaff, user.RoleUser} {
		if admits("X-Probe-Session", string(role)+":mfa") {
			ra.roles = append(ra.roles, string(role))
			if owner == "" {
				owner = role
			}
			if !admits("X-Probe-Session", string(role)+":nomfa") {
				ra.mfa = true
			}
		}
	}
	if owner == "" {
		return ra
	}
	if ra.apiKey = admits("Authorization", apiKey(owner, all)); ra.apiKey {
		ra.keyScopes = minimalScopes(all, func(sc []string) bool { return admits("Authorization", apiKey(owner, sc)) })
	}
	if ra.oauth = admits("Authorization", bearer(all)); ra.oauth {
		ra.oauthScopes = minimalScopes(all, func(sc []string) bool { return admits("Authorization", bearer(sc)) })
	}
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
			problems = append(problems, o.String()+": "+fmt.Sprintf(format, args...))
		}
		authAgrees := true
		got := securityOf(o.op)
		want := expectedSecurity(ra)
		if !slices.Equal(got, want) {
			bad("security is %v; the router admits %v", got, want)
			authAgrees = false
		}
		gotRoles := stringList(o.op["x-ghd-roles"])
		if !slices.Equal(gotRoles, ra.roles) {
			bad("x-ghd-roles is %v; the router admits sessions with roles %v", gotRoles, ra.roles)
			authAgrees = false
		}
		gotMFA, _ := o.op["x-ghd-mfa"].(bool)
		if _, present := o.op["x-ghd-mfa"]; present != (len(ra.roles) > 0) || gotMFA != ra.mfa {
			bad("x-ghd-mfa is %v; the router requires a passed second factor: %v", o.op["x-ghd-mfa"], ra.mfa)
			authAgrees = false
		}
		// Who may call it, in the first sentence, where not everyone may.
		// Only once the flags above agree with the router: when they do not,
		// that is the cause to report, and the sentence follows from them.
		if authAgrees && len(ra.roles) > 0 && len(ra.roles) < len(specRoles) {
			want := callerSentence(ra.roles, ra.mfa, ra.apiKey || ra.oauth)
			if d, _ := o.op["description"].(string); !strings.HasPrefix(strings.TrimSpace(d), want) {
				bad("the description of an operation open to some roles only starts by saying so: %q", want)
			}
		}
		when, _ := o.op["x-ghd-anonymous-when"].(string)
		if (when == "guest_submission_enabled") != ra.anonymousWhenGuests || (when != "" && !ra.anonymousWhenGuests) {
			bad("x-ghd-anonymous-when is %q; the router admits anonymous callers when guests are enabled: %v", when, ra.anonymousWhenGuests)
		}
		resps, _ := o.op["responses"].(map[string]any)
		has := func(code string) bool { _, ok := resps[code]; return ok }
		refIs := func(code, name string) bool {
			m, _ := resps[code].(map[string]any)
			return m["$ref"] == responsesPrefix+name
		}
		gated := !ra.public && !ra.guest
		if gated && !has("401") {
			bad("an authenticated operation must document 401")
		}
		if ra.anonStatus != 0 && ra.anonStatus != http.StatusUnauthorized {
			bad("a caller with no credential is answered %d, not 401; the role check has to run before RequireMFA and the other gates", ra.anonStatus)
		}
		var refused []int
		for code := range ra.refusals {
			if code != http.StatusUnauthorized && !has(fmt.Sprint(code)) { // 401: reported above
				refused = append(refused, code)
			}
		}
		sort.Ints(refused)
		for _, code := range refused {
			bad("the router refuses some callers with %d (role, MFA, scope or machine credential); document it", code)
		}
		if ra.ticketAccess && !refIs("404", "TicketNotFound") {
			bad("routes under /tickets/{id} answer 404 for a ticket the caller may not see; 404 must be $ref TicketNotFound")
		}
		if ra.ticketAccess && !declaresParamRef(o, ticketRefParam) {
			bad("routes under /tickets/{id} take {id} as $ref TicketRef, which says what the router does with a UUID and with a tracking number")
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

// securityOf renders an operation's security requirements as sorted strings,
// one per requirement object, such as "apiKey:tickets:read", "sessionCookie",
// "anonymous". An object naming two schemes, which would mean a caller needs
// both, renders as "apiKey:tickets:read+sessionCookie" and so never matches.
func securityOf(op map[string]any) []string {
	var out []string
	reqs, _ := op["security"].([]any)
	for _, req := range reqs {
		m, ok := req.(map[string]any)
		if !ok {
			out = append(out, fmt.Sprintf("invalid(%v)", req))
			continue
		}
		var parts []string
		for name, v := range m {
			s := name
			if vals := stringList(v); len(vals) > 0 {
				sort.Strings(vals)
				s += ":" + strings.Join(vals, ",")
			}
			parts = append(parts, s)
		}
		if len(parts) == 0 {
			parts = []string{"anonymous"}
		}
		sort.Strings(parts)
		out = append(out, strings.Join(parts, "+"))
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
	withScopes := func(name string, scopes []string) string {
		if len(scopes) == 0 {
			return name
		}
		return name + ":" + strings.Join(scopes, ",")
	}
	if ra.anonymousWhenGuests {
		out = append(out, "anonymous")
	}
	if len(ra.roles) > 0 {
		out = append(out, "sessionCookie")
	}
	if ra.apiKey {
		out = append(out, withScopes("apiKey", ra.keyScopes))
	}
	if ra.oauth {
		out = append(out, withScopes("oauthClient", ra.oauthScopes))
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
