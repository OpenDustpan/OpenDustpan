package conformance_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/conformance"
)

var caseName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TestCaseNames checks that each case has a known group and a kebab-case name
// that is unique in its group, so that a report names each case once.
func TestCaseNames(t *testing.T) {
	t.Parallel()

	groups := conformance.Groups()
	seen := make(map[string]bool)

	for _, c := range conformance.Cases() {
		if !slices.Contains(groups, c.Group) {
			t.Errorf("case %q: group %q is not in Groups", c.Name, c.Group)
		}

		if !caseName.MatchString(c.Name) {
			t.Errorf("case %q: name is not kebab-case", c.Name)
		}

		key := string(c.Group) + "/" + c.Name
		if seen[key] {
			t.Errorf("case %q is in the list two times", key)
		}

		seen[key] = true
	}
}

// TestEveryGroupHasACase checks that no group is empty.
func TestEveryGroupHasACase(t *testing.T) {
	t.Parallel()

	cases := conformance.Cases()
	for _, g := range conformance.Groups() {
		if !slices.ContainsFunc(cases, func(c conformance.Case) bool { return c.Group == g }) {
			t.Errorf("group %q has no case", g)
		}
	}
}

// TestEnsureProfileOnANewServerAtTheSameURL checks the seed profile of a server
// at a URL that an earlier server used. Each row is one scenario: a server that
// does not have the profile gets it again, a server that has it gets no POST,
// and a failed read of the profile is an error.
func TestEnsureProfileOnANewServerAtTheSameURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		between   func(exists *atomic.Bool, getStatus *atomic.Int64)
		wantErr   error
		wantPosts int64
	}{
		{
			name:      "new server at the same URL",
			between:   func(exists *atomic.Bool, _ *atomic.Int64) { exists.Store(false) },
			wantErr:   nil,
			wantPosts: 2,
		},
		{
			name:      "same server keeps the profile",
			between:   func(_ *atomic.Bool, _ *atomic.Int64) {},
			wantErr:   nil,
			wantPosts: 1,
		},
		{
			name: "read of the profile fails",
			between: func(_ *atomic.Bool, getStatus *atomic.Int64) {
				getStatus.Store(http.StatusInternalServerError)
			},
			wantErr:   conformance.ErrExpectation,
			wantPosts: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var sc profileSeedServer

			srv := httptest.NewServer(&sc)
			t.Cleanup(srv.Close)

			if _, err := conformance.SeedProfile(t.Context(), srv.URL); err != nil {
				t.Fatalf("first seed: %v", err)
			}

			tc.between(&sc.exists, &sc.getStatus)

			etag, err := conformance.SeedProfile(t.Context(), srv.URL)
			checkSecondSeed(t, etag, err, tc.wantErr)

			if got := sc.posts.Load(); got != tc.wantPosts {
				t.Errorf("the seed profile was posted %d times, want %d", got, tc.wantPosts)
			}
		})
	}
}

// profileSeedServer is a server for the seed profile. It has the profile when exists
// is true. A non-zero getStatus is the status of each GET.
type profileSeedServer struct {
	exists    atomic.Bool
	posts     atomic.Int64
	getStatus atomic.Int64
}

// ServeHTTP answers a POST that makes the profile once, and a GET that reads it.
func (s *profileSeedServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.posts.Add(1)

		if s.exists.Load() {
			w.WriteHeader(http.StatusConflict)

			return
		}

		s.exists.Store(true)
		w.WriteHeader(http.StatusCreated)

		return
	}

	if status := s.getStatus.Load(); status != 0 {
		w.WriteHeader(int(status))

		return
	}

	if !s.exists.Load() {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	w.Header().Set("ETag", `"1"`)
	w.WriteHeader(http.StatusOK)
}

// checkSecondSeed checks the answer of the second call of SeedProfile: the error
// that the row expects, or the ETag of the profile when it expects none.
func checkSecondSeed(t *testing.T, etag string, err, wantErr error) {
	t.Helper()

	if wantErr != nil {
		if !errors.Is(err, wantErr) {
			t.Fatalf("second seed: err = %v, want %v", err, wantErr)
		}

		if etag != "" {
			t.Errorf("second seed: etag %q, want none", etag)
		}

		return
	}

	if err != nil || etag != `"1"` {
		t.Fatalf("second seed: etag %q, err %v", etag, err)
	}
}

// seedExistingCase is one row of TestSeedProfileTwiceAgainstAnExistingProfile.
type seedExistingCase struct {
	name       string
	getStatus  int
	getETag    string
	postStatus int
	wantErr    error
}

// seedExistingCases are the rows of TestSeedProfileTwiceAgainstAnExistingProfile.
func seedExistingCases() []seedExistingCase {
	return []seedExistingCase{
		{
			name:       "the profile exists with a usable ETag",
			getStatus:  http.StatusOK,
			getETag:    `"1"`,
			postStatus: http.StatusConflict,
			wantErr:    nil,
		},
		{
			name:       "the profile exists without an ETag",
			getStatus:  http.StatusOK,
			getETag:    "",
			postStatus: http.StatusConflict,
			wantErr:    conformance.ErrExpectation,
		},
		{
			name:       "the profile cannot be read",
			getStatus:  http.StatusNotFound,
			getETag:    "",
			postStatus: http.StatusConflict,
			wantErr:    conformance.ErrExpectation,
		},
		{
			name:       "the write fails for another reason",
			getStatus:  http.StatusOK,
			getETag:    `"1"`,
			postStatus: http.StatusInternalServerError,
			wantErr:    conformance.ErrExpectation,
		},
	}
}

// TestSeedProfileTwiceAgainstAnExistingProfile checks the 409 of the seed
// write: the profile counts as written only when the server has it and answers
// with a usable ETag. Both runs of the seed setup must succeed then.
func TestSeedProfileTwiceAgainstAnExistingProfile(t *testing.T) {
	t.Parallel()

	for _, tc := range seedExistingCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSeedTwice(t, tc)
		})
	}
}

// checkSeedTwice runs the seed setup twice against a server that answers with
// the row, and checks the answer of each run.
func checkSeedTwice(t *testing.T, tc seedExistingCase) {
	t.Helper()

	var posts atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(tc.postStatus)

			return
		}

		if tc.getETag != "" {
			w.Header().Set("ETag", tc.getETag)
		}

		w.WriteHeader(tc.getStatus)
	}))
	t.Cleanup(srv.Close)

	etag, err := conformance.SeedProfile(t.Context(), srv.URL)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) || etag != "" {
			t.Fatalf("seed: etag %q, err %v, want %v", etag, err, tc.wantErr)
		}

		return
	}

	if err != nil || etag != `"1"` {
		t.Fatalf("seed: etag %q, err %v", etag, err)
	}

	// The second run finds the profile, so it writes nothing.
	etag, err = conformance.SeedProfile(t.Context(), srv.URL)
	if err != nil || etag != `"1"` {
		t.Fatalf("second seed: etag %q, err %v", etag, err)
	}

	if got := posts.Load(); got != 1 {
		t.Errorf("the seed profile was posted %d times, want 1", got)
	}
}
