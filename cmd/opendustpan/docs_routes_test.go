package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// docRouteMark is the text that marks a documented route as not served yet.
const docRouteMark = "(not available yet)"

const docTestToken = "doc-route-check-token-0123456789abcdef"

// docRoute is one method and path that the user documentation names, with
// the file and the line that names it.
type docRoute struct {
	method string
	path   string
	file   string
	line   int
	marked bool
}

var (
	docMethodPath = regexp.MustCompile("`([A-Z]+) ((?:/[^`\\s<>?]*|<[^`>]*>)+)(?:\\?[^`\\s]*)?`")
	docMethodCell = regexp.MustCompile("`([A-Z]+)`")
	docPathCell   = regexp.MustCompile("`((?:/[^`\\s<>?]*|<[^`>]*>)+)(?:\\?[^`\\s]*)?`")
	docPrefixSent = regexp.MustCompile("(?i)(?:the paths? .* start with|relative to) `([^`]+)`")
	docCurlURL    = regexp.MustCompile(`\$SERVER(/[^\s"']*)`)
	docCurlMethod = regexp.MustCompile(`-X\s+([A-Z]+)`)
	docVar        = regexp.MustCompile(`\$[A-Z_]+`)
	docBraces     = regexp.MustCompile(`\{[^}/]+\}`)
	docAngles     = regexp.MustCompile(`<[^>/]+>`)
)

// docPage is the state of reading one documentation page.
type docPage struct {
	path          string
	headingMarked bool
	// headingLevel is the level of the last heading, 0 before the first one.
	headingLevel int
	prefix       string
	// prefixLevel is the level of the heading under which prefix was stated,
	// 0 for a sentence before the first heading (the page prefix).
	prefixLevel int
	out         []docRoute
}

// parseDocContent reads the routes of one documentation page. content is the
// page text and path names the page.
func parseDocContent(path, content string) []docRoute {
	page := docPage{path: path, headingMarked: false, headingLevel: 0, prefix: "", prefixLevel: 0, out: nil}
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		page.addLine(i+1, line)
	}

	// Curl URLs start at the server root, so they take no page prefix.
	page.out = append(page.out, curlDocRoutes(path, content)...)

	kept := page.out[:0]

	for _, r := range page.out {
		if !strings.Contains(r.path, "...") {
			kept = append(kept, r)
		}
	}

	return kept
}

// addLine reads the routes of one line of the page.
func (p *docPage) addLine(line int, text string) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "#") {
		p.headingMarked = strings.Contains(text, docRouteMark)
		level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		p.headingLevel = level
		// A heading of the same or a higher level ends the section that set
		// the prefix. The page prefix (prefixLevel 0) lasts for the whole page.
		if p.prefixLevel > 0 && level <= p.prefixLevel {
			p.prefix = ""
			p.prefixLevel = 0
		}
	}

	if m := docPrefixSent.FindStringSubmatch(text); m != nil {
		p.prefix = m[1]
		p.prefixLevel = p.headingLevel
	}

	marked := p.headingMarked || strings.Contains(text, docRouteMark)
	p.out = append(p.out, p.prefixRoutes(tableDocRoutes(p.path, line, text, marked))...)

	if strings.Contains(text, "|") {
		return
	}

	p.out = append(p.out, p.prefixRoutes(spanDocRoutes(p.path, line, text, marked))...)
}

// prefixRoutes gives each relative path the prefix in scope. A path that starts
// with /api/ is absolute and takes no prefix. A path that already starts with
// the prefix is left as it is.
func (p *docPage) prefixRoutes(routes []docRoute) []docRoute {
	for i := range routes {
		if p.prefix != "" && !strings.HasPrefix(routes[i].path, "/api/") &&
			!strings.HasPrefix(routes[i].path, p.prefix) {
			routes[i].path = p.prefix + routes[i].path
		}
	}

	return routes
}

// tableDocRoutes reads the routes of one Markdown table row. The path cell
// and the method cell may come in either order, or one cell may hold both.
func tableDocRoutes(path string, line int, row string, marked bool) []docRoute {
	cells := strings.Split(row, "|")
	if len(cells) < 3 {
		return nil
	}

	out := tableCombinedRoutes(path, line, row, marked)
	if len(out) > 0 {
		return out
	}

	return tableSplitRoutes(path, line, cells, marked)
}

// tableCombinedRoutes reads the rows with a method and a path in one cell.
func tableCombinedRoutes(path string, line int, row string, marked bool) []docRoute {
	var out []docRoute

	for _, s := range docMethodPath.FindAllStringSubmatch(row, -1) {
		method, routePath := s[1], s[2]
		if !isDocMethod(method) || !strings.HasPrefix(routePath, "/") {
			continue
		}

		out = append(out, docRoute{method: method, path: routePath, file: path, line: line, marked: marked})
	}

	return out
}

// tableSplitRoutes reads the rows with a path cell and a method cell.
func tableSplitRoutes(path string, line int, cells []string, marked bool) []docRoute {
	methods := tableRowMethods(cells)
	if len(methods) == 0 {
		return nil
	}

	var out []docRoute

	for _, cell := range cells {
		out = append(out, tableCellRoutes(path, line, cell, methods, marked)...)
	}

	return out
}

// tableRowMethods lists the HTTP methods that the cells of a table row name.
func tableRowMethods(cells []string) []string {
	var methods []string

	for _, cell := range cells {
		for _, m := range docMethodCell.FindAllStringSubmatch(cell, -1) {
			if isDocMethod(m[1]) {
				methods = append(methods, m[1])
			}
		}
	}

	return methods
}

// tableCellRoutes reads the documented routes of one table cell for each of
// the methods that the row names.
func tableCellRoutes(path string, line int, cell string, methods []string, marked bool) []docRoute {
	var out []docRoute

	for _, m := range docPathCell.FindAllStringSubmatch(cell, -1) {
		routePath := m[1]
		if !strings.HasPrefix(routePath, "/") {
			continue
		}

		for _, method := range methods {
			r := docRoute{method: method, path: routePath, file: path, line: line, marked: marked}
			out = append(out, r)
		}
	}

	return out
}

// spanDocRoutes reads the inline method and path pairs of one line, such as
// a list item. Table rows are read by tableDocRoutes instead.
func spanDocRoutes(path string, line int, text string, marked bool) []docRoute {
	var out []docRoute

	for _, m := range docMethodPath.FindAllStringSubmatch(text, -1) {
		if !isDocMethod(m[1]) || !strings.HasPrefix(m[2], "/") {
			continue
		}

		out = append(out, docRoute{method: m[1], path: m[2], file: path, line: line, marked: marked})
	}

	return out
}

// curlDocRoutes reads the curl blocks of one page. The method comes from -X
// and defaults to GET. The URL follows $SERVER.
func curlDocRoutes(path, content string) []docRoute {
	var out []docRoute

	lines := strings.Split(content, "\n")
	inBlock := false
	var block []string

	blockLine := 0

	flush := func() {
		if len(block) > 0 {
			out = append(out, parseCurlBlock(path, blockLine, block)...)
		}

		block = nil
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inBlock {
				flush()
			}

			inBlock = !inBlock

			continue
		}

		if !inBlock {
			continue
		}

		if len(block) == 0 {
			blockLine = i + 1
		}

		block = append(block, line)
	}

	flush()

	return out
}

// parseCurlBlock reads the documented routes of one fenced block. A block
// without $SERVER names no route.
func parseCurlBlock(path string, line int, block []string) []docRoute {
	joined := strings.Join(block, "\n")
	if !strings.Contains(joined, "$SERVER") {
		return nil
	}

	method := http.MethodGet
	if m := docCurlMethod.FindStringSubmatch(joined); m != nil {
		method = m[1]
	}

	if !isDocMethod(method) {
		return nil
	}

	marked := strings.Contains(joined, docRouteMark)

	var out []docRoute

	for _, m := range docCurlURL.FindAllStringSubmatch(joined, -1) {
		routePath := docVar.ReplaceAllString(m[1], "zz")
		routePath = strings.SplitN(routePath, "?", 2)[0]
		out = append(out, docRoute{method: method, path: routePath, file: path, line: line, marked: marked})
	}

	return out
}

// isDocMethod reports whether name is an HTTP method that the docs name.
func isDocMethod(name string) bool {
	switch name {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// extractDocRoutes reads the documented routes of the reference and the
// guides of the repository at root.
func extractDocRoutes(root string) ([]docRoute, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open repository root: %w", err)
	}

	out, err := extractDocDirs(opened)
	closeErr := opened.Close()

	if err != nil {
		return nil, err
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close repository root: %w", closeErr)
	}

	return out, nil
}

// extractDocDirs reads the documented routes of the reference and the guides.
func extractDocDirs(opened *os.Root) ([]docRoute, error) {
	var out []docRoute

	for _, dir := range []string{"docs/reference", "docs/guides"} {
		routes, err := extractDocDir(opened, dir)
		if err != nil {
			return nil, err
		}

		out = append(out, routes...)
	}

	return out, nil
}

// extractDocDir reads the documented routes of the Markdown pages of one
// directory of the opened repository root.
func extractDocDir(opened *os.Root, dir string) ([]docRoute, error) {
	dirHandle, err := opened.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open directory %s: %w", dir, err)
	}

	entries, err := dirHandle.ReadDir(-1)
	closeErr := dirHandle.Close()

	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", dir, err)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close directory %s: %w", dir, closeErr)
	}

	var out []docRoute

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		rel := filepath.Join(dir, entry.Name())

		data, err := readRootFile(opened, rel)
		if err != nil {
			return nil, err
		}

		out = append(out, parseDocContent(rel, string(data))...)
	}

	return out, nil
}

// readRootFile reads one file under the opened repository root.
func readRootFile(opened *os.Root, name string) ([]byte, error) {
	handle, err := opened.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open page %s: %w", name, err)
	}

	data, err := io.ReadAll(handle)
	closeErr := handle.Close()

	if err != nil {
		return nil, fmt.Errorf("read page %s: %w", name, err)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close page %s: %w", name, closeErr)
	}

	return data, nil
}

// routeIsMarked reports whether the docs mark the route as not served yet.
func routeIsMarked(r docRoute) bool {
	return r.marked
}

// normalizeDocPath replaces the placeholders of a documented route with a
// fixed name, so the check can send the route.
func normalizeDocPath(path string) string {
	routePath := docBraces.ReplaceAllString(path, "zz")

	return docAngles.ReplaceAllString(routePath, "zz")
}

// checkDocRoutes sends each documented route to handler with no credential
// and returns one line per route that no route serves and no marker covers.
func checkDocRoutes(ctx context.Context, handler http.Handler, routes []docRoute) []string {
	var problems []string

	seen := map[string]bool{}

	for _, r := range routes {
		if routeIsMarked(r) {
			continue
		}

		key := r.method + " " + r.path
		if seen[key] {
			continue
		}

		seen[key] = true
		problems = append(problems, checkOneDocRoute(ctx, handler, r)...)
	}

	return problems
}

// checkOneDocRoute sends one documented route to handler with no credential.
// It returns one line when no route serves the path and no marker covers it.
func checkOneDocRoute(ctx context.Context, handler http.Handler, r docRoute) []string {
	routePath := normalizeDocPath(r.path)
	at := r.file + ":" + strconv.Itoa(r.line) + ": " + r.method + " " + r.path

	req, err := http.NewRequestWithContext(ctx, r.method, "http://example.invalid"+routePath, http.NoBody)
	if err != nil {
		return []string{at + ": new request: " + err.Error()}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		return []string{at + ": read body: " + err.Error()}
	}

	if err := rec.Result().Body.Close(); err != nil {
		return []string{at + ": close body: " + err.Error()}
	}

	if strings.Contains(string(body), "server.route-not-found") {
		return []string{at + " answers server.route-not-found"}
	}

	return nil
}

// repoRoot returns the repository root above this file.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller of the test file is unknown")
	}

	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// docTestServer builds a server with each optional feature turned on, so a
// documented route that any option mounts is served.
func docTestServer(t *testing.T, issuer string) *dustpan.Server {
	t.Helper()

	ctx := t.Context()

	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	backupTarget, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := backupTarget.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", docTestToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	bindPassword := docBindPassword(t)

	opts := []dustpan.Option{
		dustpan.WithInstallation(namespace.InstallationName("local")),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithSelfServiceBlueprints(st.SelfServiceBlueprints()),
		dustpan.WithPublicURL("https://dustpan.example.invalid"),
		dustpan.WithDurableStore(st.DurableOperations()),
		dustpan.WithJobStore(st.Jobs()),
		dustpan.WithDriftStore(st.DriftResults()),
		dustpan.WithTriggerStore(st.Triggers()),
		dustpan.WithUnitSettingsStore(st.UnitSettings()),
		dustpan.WithRunners(st.Runners()),
		dustpan.WithSourceFilters(st.SourceFilters()),
		dustpan.WithTrustRules(st.TrustRules()),
		dustpan.WithSavedViewStore(st.SavedViews()),
		dustpan.WithSettings(st.SettingStore(), settingtest.Declarations(), nil),
		dustpan.WithProfileStore(st.Profiles()),
		dustpan.WithIssuerStore(st.Issuer()),
		dustpan.WithGitHostStore(st.GitHost()),
		dustpan.WithRegistry(st.Registry(), st.RegistryKeys()),
		dustpan.WithSinkStore(st.NotificationSinks()),
		dustpan.WithIdempotencyStore(st.Idempotency(wrapper)),
		dustpan.WithInstanceStore(st),
		dustpan.WithIPAllowlists(true),
		dustpan.WithIPAllowlistStore(st),
		dustpan.WithBackupTarget(backupTarget, "dir:backup-target"),
		dustpan.WithLDAPProvider(dustpan.LDAPProvider{
			RootCAs: nil, AllowedRanges: nil, ID: "corp-ldap", DisplayName: "Corp",
			URL: "ldaps://ldap.example.invalid", BindDN: "cn=dustpan,ou=services,dc=example,dc=org",
			BindPassword: bindPassword, BaseDN: "ou=people,dc=example,dc=org", UserFilter: "(uid={user})",
			UserNameAttribute: "uid", EmailAttribute: "mail", NameAttribute: "cn", Subject: "entryUUID",
			DisabledAttribute: "", Groups: "off", GroupBaseDN: "", GroupFilter: "", GroupMemberAttribute: "",
			GroupMemberOfAttribute: "", GroupNameAttribute: "", DisabledValues: nil, DisabledMask: 0,
			StartTLS: false, AutoCreate: true, SubjectFailureLimit: 5, SubjectFailureLimitSource: "default",
		}),
		dustpan.WithOIDCProvider(dustpan.OIDCProvider{
			HTTPClient: nil, ID: "corp-oidc", DisplayName: "Corp", Issuer: issuer,
			ClientID: "dustpan", ClientSecret: "doc-route-check", Scopes: nil, AutoCreate: true,
		}),
	}

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start server: %v", err)
	}

	return srv
}

// docBindPassword builds the bind password of the LDAP test provider from
// its parts at run time, so the source holds no credential text.
func docBindPassword(t *testing.T) session.Password {
	t.Helper()

	raw := fakecred.URL("ldaps", "dustpan", "bind-password-of-the-docs-test", "ldap.example.invalid", "services")

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse credential: %v", err)
	}

	password, _ := parsed.User.Password()

	return session.NewPassword(password)
}

func TestExtractDocRoutes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
		doc  string
		want []docRoute
	}{
		{
			name: "table path then method",
			path: "docs/reference/example.md",
			doc: "| Route | Method |\n| --- | --- |\n" +
				"| `/api/dustpan/v1/self-service/blueprints/{namespace}` | `GET` | `x` |\n",
			want: []docRoute{{
				method: "GET", path: "/api/dustpan/v1/self-service/blueprints/{namespace}",
				file: "docs/reference/example.md", line: 3, marked: false,
			}},
		},
		{
			name: "table method and path",
			path: "docs/reference/example.md",
			doc: "| Method | Route |\n| --- | --- |\n" +
				"| `PUT` | `/api/dustpan/v1/things/{id}` |\n",
			want: []docRoute{{
				method: "PUT", path: "/api/dustpan/v1/things/{id}",
				file: "docs/reference/example.md", line: 3, marked: false,
			}},
		},
		{
			name: "inline method and path",
			path: "docs/reference/example.md",
			doc:  "- `POST /api/dustpan/v1/things` stores one thing.\n",
			want: []docRoute{{
				method: "POST", path: "/api/dustpan/v1/things",
				file: "docs/reference/example.md", line: 1, marked: false,
			}},
		},
		{
			name: "curl server with flag",
			path: "docs/guides/example.md",
			doc:  "```sh\ncurl -sS -X PUT \\\n  \"$SERVER/api/dustpan/v1/things/$NAMESPACE/draft\"\n```\n",
			want: []docRoute{{
				method: "PUT", path: "/api/dustpan/v1/things/zz/draft",
				file: "docs/guides/example.md", line: 2, marked: false,
			}},
		},
		{
			name: "relative path with page prefix",
			path: "docs/reference/example.md",
			doc: "The paths of this API start with `/api/dustpan/v1`.\n\n" +
				"- `GET /namespaces/{namespace}/units` lists the units.\n",
			want: []docRoute{{
				method: "GET", path: "/api/dustpan/v1/namespaces/{namespace}/units",
				file: "docs/reference/example.md", line: 3, marked: false,
			}},
		},
		{
			name: "relative path with relative-to base",
			path: "docs/reference/example.md",
			doc: "The paths of the API start with `/api/dustpan/v1`. Each path in this table is\n" +
				"relative to `/api/dustpan/v1/namespaces/{namespace}/registry`.\n\n" +
				"| Method and path | Action |\n| --- | --- |\n" +
				"| `GET /v1/providers/{type}/versions` | `registry:read` |\n",
			want: []docRoute{{
				method: "GET", path: "/api/dustpan/v1/namespaces/{namespace}/registry/v1/providers/{type}/versions",
				file: "docs/reference/example.md", line: 6, marked: false,
			}},
		},
		{
			name: "ellipsis path is skipped",
			path: "docs/reference/example.md",
			doc:  "- `GET .../draft` returns the draft.\n",
			want: nil,
		},
		{
			name: "marker",
			path: "docs/reference/example.md",
			doc: "| Route | Method |\n| --- | --- |\n" +
				"| `GET /api/dustpan/v1/later` (not available yet) | `GET` |\n",
			want: []docRoute{{
				method: "GET", path: "/api/dustpan/v1/later",
				file: "docs/reference/example.md", line: 3, marked: true,
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := parseDocContent(tc.path, tc.doc)

			if len(got) != len(tc.want) {
				t.Fatalf("routes = %+v, want %+v", got, tc.want)
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("route %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestDocsRouteCheckFailsOnAnUnservedRoute(t *testing.T) {
	t.Parallel()

	fake := func(marked bool) []docRoute {
		return []docRoute{{
			method: "GET", path: "/api/dustpan/v1/no-such-route-zz",
			file: "docs/reference/fake.md", line: 7, marked: marked,
		}}
	}

	handler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)

		if _, err := w.Write([]byte(`{"code":"server.route-not-found"}`)); err != nil {
			panic("write fake answer: " + err.Error())
		}
	}))
	defer handler.Close()

	serve := func(routes []docRoute) []string {
		return checkDocRoutes(t.Context(), handler.Config.Handler, routes)
	}

	if problems := serve(fake(false)); len(problems) != 1 {
		t.Fatalf("unmarked fake route: problems = %v, want one", problems)
	}

	if problems := serve(fake(true)); len(problems) != 0 {
		t.Fatalf("marked fake route: problems = %v, want none", problems)
	}
}

func TestDocsRoutesAreServed(t *testing.T) {
	t.Parallel()

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer issuer.Close()

	srv := docTestServer(t, issuer.URL)

	routes, err := extractDocRoutes(repoRoot(t))
	if err != nil {
		t.Fatalf("extract doc routes: %v", err)
	}

	if len(routes) == 0 {
		t.Fatal("the docs name no route")
	}

	names := map[string]bool{}

	for _, r := range routes {
		if routeIsMarked(r) {
			t.Logf("marked route: %s:%d %s %s", r.file, r.line, r.method, r.path)
		}

		if r.method == "" || r.path == "" {
			t.Fatalf("route without method or path: %+v", r)
		}

		names[r.method+" "+normalizeDocPath(r.path)] = true
	}

	for _, want := range []string{
		"GET /api/dustpan/v1/self-service/blueprints/zz",
		"GET /api/dustpan/v1/self-service/blueprints/zz/zz/draft",
		"PUT /api/dustpan/v1/self-service/blueprints/zz/zz/draft",
		"POST /api/dustpan/v1/self-service/blueprints/zz/zz/versions",
		"GET /api/dustpan/v1/self-service/blueprints/zz/zz/versions/zz",
		"POST /api/dustpan/v1/self-service/blueprints/zz/zz/versions/zz/retire",
		"GET /api/server/sign-in-providers",
		"GET /api/server/sign-in-providers/zz",
		"POST /api/server/sign-in-providers",
		"PATCH /api/server/sign-in-providers/zz",
		"DELETE /api/server/sign-in-providers/zz",
		"POST /api/dustpan/v1/sign-in/passkey/begin",
		"POST /api/dustpan/v1/sign-in/passkey/finish",
		"GET /api/server/session",
		"POST /api/server/sign-out",
		"POST /api/dustpan/v1/me/passkeys/register/begin",
		"POST /api/dustpan/v1/me/passkeys/register/finish",
		"GET /api/dustpan/v1/me/passkeys",
		"DELETE /api/dustpan/v1/me/passkeys/zz",
		"GET /api/server/sign-in/zz",
		"POST /api/dustpan/v1/sign-in/providers/corp-ldap/password",
	} {
		if !names[want] {
			t.Fatalf("the docs name no route %q", want)
		}
	}

	if problems := checkDocRoutes(t.Context(), srv.Handler(), routes); len(problems) > 0 {
		t.Fatalf("documented routes that no route serves:\n%s", strings.Join(problems, "\n"))
	}
}
