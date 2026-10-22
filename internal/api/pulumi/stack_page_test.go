package pulumi_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// stackPageBody is one decoded page of GET /api/user/stacks, with the raw
// token so a test can tell null from a string.
type stackPageBody struct {
	ContinuationToken *string          `json:"continuationToken"`
	Stacks            []stackPageEntry `json:"stacks"`
}

// stackPageEntry is one decoded stack of a page.
type stackPageEntry struct {
	OrgName     string `json:"orgName"`
	ProjectName string `json:"projectName"`
	StackName   string `json:"stackName"`
}

// manyStacks returns a handler over n stacks in each of the namespaces,
// named p<i%3>/s<i>, with the authorizer authz.
func manyStacks(t *testing.T, n int, authz identity.Authorizer, namespaces ...string) *pulumi.Handler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, namespaces...)

	for _, ns := range namespaces {
		for i := range n {
			seedStack(t, stStore, ns, fmt.Sprintf("p%d", i%3), fmt.Sprintf("s%04d", i))
		}
	}

	return newUserHandler(t, nsStore, stStore, authz)
}

// readStackPage reads one page and fails the test on a status other than
// 200.
func readStackPage(t *testing.T, handler *pulumi.Handler, target string) stackPageBody {
	t.Helper()

	rec := get(t, handler, target)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answers %d, want 200 (%s)", target, rec.Code, rec.Body.String())
	}

	var page stackPageBody
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}

	return page
}

// walkStacks reads every page of target and returns the keys of the stacks
// and the size of each page.
func walkStacks(t *testing.T, handler *pulumi.Handler, target string) ([]string, []int) {
	t.Helper()

	var (
		keys  []string
		sizes []int
		token *string
	)

	for range 20 {
		next := target
		if token != nil {
			next += "&continuationToken=" + url.QueryEscape(*token)
		}

		page := readStackPage(t, handler, next)
		sizes = append(sizes, len(page.Stacks))

		for _, st := range page.Stacks {
			keys = append(keys, st.OrgName+"/"+st.ProjectName+"/"+st.StackName)
		}

		if page.ContinuationToken == nil {
			return keys, sizes
		}

		token = page.ContinuationToken
	}

	t.Fatalf("GET %s did not end after 20 pages", target)

	return nil, nil
}

// TestListStacksPages proves that the stack list pages with
// continuationToken: 1,201 stacks give pages of 500, 500 and 201, each stack
// once in the order of namespace, project and name, and null on the last
// page. It holds with and without the organization and the project.
func TestListStacksPages(t *testing.T) {
	t.Parallel()

	handler := manyStacks(t, 1201, selectiveAuthorizer{deny: map[identity.Action]bool{}}, "acme")

	for _, tc := range []struct {
		target string
		sizes  []int
	}{
		{target: "/api/user/stacks?organization=acme", sizes: []int{500, 500, 201}},
		{target: "/api/user/stacks?x=1", sizes: []int{500, 500, 201}},
		{target: "/api/user/stacks?organization=acme&project=p1", sizes: []int{400}},
	} {
		keys, sizes := walkStacks(t, handler, tc.target)
		if !slices.Equal(sizes, tc.sizes) {
			t.Fatalf("%s: page sizes = %v, want %v", tc.target, sizes, tc.sizes)
		}

		if !slices.IsSorted(keys) || len(slices.Compact(slices.Clone(keys))) != len(keys) {
			t.Fatalf("%s: stacks are not each once in key order", tc.target)
		}
	}
}

// TestListStacksPagesReadableNamespaces proves that the list with no
// organization pages over the readable namespaces only.
func TestListStacksPagesReadableNamespaces(t *testing.T) {
	t.Parallel()

	handler := manyStacks(t, 300, denyBeta{}, "acme", "beta", "zeta")

	keys, sizes := walkStacks(t, handler, "/api/user/stacks?x=1")
	if !slices.Equal(sizes, []int{500, 100}) {
		t.Fatalf("page sizes = %v, want [500 100]", sizes)
	}

	for _, k := range keys {
		if k[:5] == "beta/" {
			t.Fatalf("the list holds %s, want no stack of beta", k)
		}
	}
}

// encodeToken returns a token of the wire shape with the fields of w.
func encodeToken(t *testing.T, w map[string]string) string {
	t.Helper()

	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("encode token: %v", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw)
}

// TestListStacksRefusesBadToken proves the check of the token alone: a
// token that does not decode, a token of another query, and a token with a
// name that is not valid each answer 400 in the error shape of the API.
func TestListStacksRefusesBadToken(t *testing.T) {
	t.Parallel()

	handler := manyStacks(t, 501, selectiveAuthorizer{deny: map[identity.Action]bool{}}, "acme", "beta")

	page := readStackPage(t, handler, "/api/user/stacks?organization=acme")
	if page.ContinuationToken == nil {
		t.Fatal("first page has no token, want one")
	}

	good := *page.ContinuationToken

	raw, err := base64.RawURLEncoding.DecodeString(good)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}

	var wire map[string]string
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode token JSON: %v", err)
	}

	badName := map[string]string{"n": "acme", "p": "../x", "s": "s0001", "f": wire["f"]}
	tamperedFingerprint := map[string]string{"n": wire["n"], "p": wire["p"], "s": wire["s"], "f": "0000000000000000"}
	// The fingerprint is not keyed: a token with the fingerprint of the query
	// and the key of another place must still answer 400.
	otherNamespace := map[string]string{"n": "beta", "p": wire["p"], "s": wire["s"], "f": wire["f"]}
	missingNamespace := map[string]string{"n": "zzz", "p": wire["p"], "s": wire["s"], "f": wire["f"]}
	otherProject := map[string]string{"n": "acme", "p": "p2", "s": "s0001", "f": testFingerprint("acme", "p1")}
	otherRoute := map[string]string{"cursor": "abc", "mode": "history", "n": "acme"}
	org := "/api/user/stacks?organization=acme&continuationToken="

	for _, tc := range []struct{ name, target string }{
		{name: "not base64", target: "/api/user/stacks?organization=acme&continuationToken=%25%25"},
		{name: "not JSON", target: "/api/user/stacks?organization=acme&continuationToken=" + base64.RawURLEncoding.EncodeToString([]byte("x"))},
		{name: "another organization", target: "/api/user/stacks?organization=beta&continuationToken=" + good},
		{name: "another project", target: "/api/user/stacks?organization=acme&project=p1&continuationToken=" + good},
		{name: "no organization", target: "/api/user/stacks?continuationToken=" + good},
		{name: "a name that is not valid", target: org + encodeToken(t, badName)},
		{name: "tampered fingerprint", target: org + encodeToken(t, tamperedFingerprint)},
		{name: "key in another namespace", target: org + encodeToken(t, otherNamespace)},
		{name: "key in a namespace that does not exist", target: org + encodeToken(t, missingNamespace)},
		{
			name:   "key in another project",
			target: "/api/user/stacks?organization=acme&project=p1&continuationToken=" + encodeToken(t, otherProject),
		},
		{name: "token of another route", target: org + encodeToken(t, otherRoute)},
	} {
		rec := get(t, handler, tc.target)

		var body struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusBadRequest ||
			body.Code != http.StatusBadRequest {
			t.Fatalf("%s: answer %d %s, want 400 with code 400", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// testFingerprint returns the fingerprint that the stack list gives a query:
// the first 8 bytes of the SHA-256 of the organization and the project, in hex.
func testFingerprint(organization, project string) string {
	sum := sha256.Sum256([]byte("organization=" + organization + "\nproject=" + project))

	return hex.EncodeToString(sum[:8])
}

// TestListStacksAcceptsBoundToken proves that a token whose key lies in the
// organization and the project of the query still pages.
func TestListStacksAcceptsBoundToken(t *testing.T) {
	t.Parallel()

	handler := manyStacks(t, 501, selectiveAuthorizer{deny: map[identity.Action]bool{}}, "acme", "beta")
	token := encodeToken(t, map[string]string{"n": "acme", "p": "p1", "s": "s0001", "f": testFingerprint("acme", "p1")})

	page := readStackPage(t, handler, "/api/user/stacks?organization=acme&project=p1&continuationToken="+token)
	if len(page.Stacks) == 0 {
		t.Fatal("page after a bound token holds no stack")
	}

	for _, st := range page.Stacks {
		if st.OrgName != "acme" || st.ProjectName != "p1" {
			t.Fatalf("page holds %s/%s, want acme/p1 only", st.OrgName, st.ProjectName)
		}
	}
}
