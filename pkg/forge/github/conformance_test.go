package github_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/forgetest"
	"opendustpan.org/opendustpan/pkg/forge/github"
)

// hostFixture is the Fixture of the conformance suite over the fake GitHub
// host of package githubfake.
type hostFixture struct {
	host *githubfake.Fake
	repo string
	auth forge.AuthKind

	// mu guards secrets, the secret of each delivery that the fixture made.
	mu      sync.Mutex
	secrets map[string][]byte
}

// partyLogin returns the login that the fake shows for a party of a pushed
// commit. A party with no account (forgetest.PartyNobody) has the empty login.
func partyLogin(x *hostFixture, p forgetest.Party) string {
	switch p {
	case forgetest.PartyOther:
		return githubfake.OtherAuthor
	case forgetest.PartyForge:
		return githubfake.WebFlow
	case forgetest.PartyConnection:
		if x.auth == forge.AuthApp {
			return x.host.AppLogin()
		}

		return x.host.UserLogin()
	case forgetest.PartyNobody:
		return ""
	}

	return ""
}

func (x *hostFixture) PushAs(
	_ context.Context, branch string, files map[string][]byte, message string, origin forgetest.Origin,
) (forge.Commit, error) {
	c, err := x.host.PushSigned(x.repo, branch, files, message, githubfake.Signature{
		Author: partyLogin(x, origin.Author), Committer: partyLogin(x, origin.Committer), Verified: origin.Verified,
	})
	if err != nil {
		return forge.Commit{}, fmt.Errorf("push as to the fake: %w", err)
	}

	return commitOf(c), nil
}

func (x *hostFixture) CommitMessage(_ context.Context, repo, commitID string) (string, error) {
	c, err := x.host.Head(repo, commitID)
	if err != nil {
		return "", fmt.Errorf("read a commit of the fake: %w", err)
	}

	return c.Message, nil
}

func (x *hostFixture) Clone(_ context.Context) (string, error) {
	owner, name, _ := strings.Cut(x.repo, "/")
	clone := owner + "/" + name + "-clone"

	if err := x.host.CloneRepo(x.repo, clone); err != nil {
		return "", fmt.Errorf("clone in the fake: %w", err)
	}

	return clone, nil
}

func (x *hostFixture) PushEntry(
	_ context.Context, branch, path string, kind forgetest.EntryKind, variant string,
) (forge.Commit, error) {
	kinds := map[forgetest.EntryKind]githubfake.EntryKind{
		forgetest.EntrySymlink:   githubfake.Symlink,
		forgetest.EntrySubmodule: githubfake.Submodule,
		forgetest.EntryDirectory: githubfake.Directory,
	}

	c, err := x.host.PushEntry(x.repo, branch, path, kinds[kind], variant)
	if err != nil {
		return forge.Commit{}, fmt.Errorf("push an entry to the fake: %w", err)
	}

	return commitOf(c), nil
}

func commentOf(c githubfake.IssueComment, review string) forge.Comment {
	return forge.Comment{
		ID: strconv.Itoa(c.ID), ReviewID: review, URL: "", AuthorID: strconv.FormatInt(c.AuthorID, 10), Body: c.Body,
		Truncated: false,
	}
}

func (x *hostFixture) Comments(_ context.Context, id string) ([]forge.Comment, error) {
	n, err := reviewNumber(id)
	if err != nil {
		return nil, err
	}

	shown, err := x.host.Comments(x.repo, n)
	if err != nil {
		return nil, fmt.Errorf("read the comments of the fake: %w", err)
	}

	out := make([]forge.Comment, 0, len(shown))
	for _, c := range shown {
		out = append(out, commentOf(c, id))
	}

	return out, nil
}

func (x *hostFixture) AddComment(_ context.Context, id, body string) (forge.Comment, error) {
	n, err := reviewNumber(id)
	if err != nil {
		return forge.Comment{}, err
	}

	c, err := x.host.AddComment(x.repo, n, githubfake.OtherAuthor, body)
	if err != nil {
		return forge.Comment{}, fmt.Errorf("add a comment to the fake: %w", err)
	}

	return commentOf(c, id), nil
}

func (x *hostFixture) Reactions(_ context.Context, commentID string) ([]string, error) {
	n, err := strconv.Atoi(commentID)
	if err != nil {
		return nil, fmt.Errorf("the comment ID %q is not a number: %w", commentID, err)
	}

	got, err := x.host.Reactions(x.repo, n)
	if err != nil {
		return nil, fmt.Errorf("read the reactions of the fake: %w", err)
	}

	return got, nil
}

func (x *hostFixture) Statuses(_ context.Context, commitID string) ([]forge.Status, error) {
	shown, err := x.host.Statuses(x.repo, commitID)
	if err != nil {
		return nil, fmt.Errorf("read the statuses of the fake: %w", err)
	}

	out := make([]forge.Status, 0, len(shown))
	for _, s := range shown {
		out = append(out, forge.Status{
			State: forge.StatusState(s.State), Context: s.Context, Description: s.Description, TargetURL: s.TargetURL,
		})
	}

	return out, nil
}

func commitOf(c githubfake.Commit) forge.Commit {
	return forge.Commit{ID: c.ID, Time: c.Time}
}

func (x *hostFixture) Push(_ context.Context, branch string, files map[string][]byte) (forge.Commit, error) {
	c, err := x.host.Push(x.repo, branch, files, "Push to "+branch)
	if err != nil {
		return forge.Commit{}, fmt.Errorf("push to the fake: %w", err)
	}

	return commitOf(c), nil
}

func (x *hostFixture) Rewrite(_ context.Context, branch string, files map[string][]byte) (forge.Commit, error) {
	c, err := x.host.Rewrite(x.repo, branch, files, "Force push to "+branch)
	if err != nil {
		return forge.Commit{}, fmt.Errorf("rewrite in the fake: %w", err)
	}

	return commitOf(c), nil
}

func (x *hostFixture) Tag(_ context.Context, name, commitID string) error {
	if err := x.host.Tag(x.repo, name, commitID); err != nil {
		return fmt.Errorf("tag in the fake: %w", err)
	}

	return nil
}

func (x *hostFixture) Protect(_ context.Context, branch string) error {
	if err := x.host.Protect(x.repo, branch); err != nil {
		return fmt.Errorf("protect in the fake: %w", err)
	}

	return nil
}

func reviewNumber(id string) (int, error) {
	n, err := strconv.Atoi(id)
	if err != nil {
		return 0, fmt.Errorf("the review ID %q is not a number: %w", id, err)
	}

	return n, nil
}

func (x *hostFixture) Merge(_ context.Context, id string) error {
	n, err := reviewNumber(id)
	if err != nil {
		return err
	}

	if mergeErr := x.host.MergePull(x.repo, n); mergeErr != nil {
		return fmt.Errorf("merge in the fake: %w", mergeErr)
	}

	return nil
}

func (x *hostFixture) Close(_ context.Context, id string) error {
	n, err := reviewNumber(id)
	if err != nil {
		return err
	}

	if closeErr := x.host.ClosePull(x.repo, n); closeErr != nil {
		return fmt.Errorf("close in the fake: %w", closeErr)
	}

	return nil
}

func (x *hostFixture) Branch(_ context.Context, name string) error {
	if err := x.host.Branch(x.repo, name); err != nil {
		return fmt.Errorf("branch in the fake: %w", err)
	}

	return nil
}

func (x *hostFixture) SetDefaultBranch(_ context.Context, branch string) error {
	if err := x.host.SetDefaultBranch(x.repo, branch); err != nil {
		return fmt.Errorf("set the default branch in the fake: %w", err)
	}

	return nil
}

// forkOwner returns the account of the person who forks the repository.
func forkOwner() githubfake.Account { return githubfake.Account{ID: 31337, Login: "forker"} }

func (x *hostFixture) ForkReview(_ context.Context, branch, target string) (string, error) {
	_, name, _ := strings.Cut(x.repo, "/")
	fork := forkOwner().Login + "/" + name

	if err := x.host.AddFork(x.repo, fork); err != nil && !errors.Is(err, githubfake.ErrExists) {
		return "", fmt.Errorf("fork in the fake: %w", err)
	}

	files := map[string][]byte{"stacks/fork.yaml": []byte("f: 1\n")}
	if _, err := x.host.Push(fork, branch, files, "Fork change"); err != nil {
		return "", fmt.Errorf("push to the fork: %w", err)
	}

	n, err := x.host.OpenPull(githubfake.PullSpec{
		Base: x.repo, BaseRef: target, Head: fork, HeadRef: branch, Title: "Change from a fork", Body: "",
		Author: forkOwner(),
	})
	if err != nil {
		return "", fmt.Errorf("open the pull request of the fork: %w", err)
	}

	return strconv.Itoa(n), nil
}

func (x *hostFixture) SetMergeState(_ context.Context, id, state string) error {
	n, err := reviewNumber(id)
	if err != nil {
		return err
	}

	if setErr := x.host.SetMergeState(x.repo, n, state); setErr != nil {
		return fmt.Errorf("set the merge state in the fake: %w", setErr)
	}

	return nil
}

// oversizeBytes is above the body limit of the egress rules.
const oversizeBytes = 5 << 20

func (x *hostFixture) Oversize(context.Context) (func(), error) {
	return x.host.Oversize(oversizeBytes), nil
}

// hookID is the ID of the hook of the events that the fixture makes.
const hookID = 1

// eventSender is the account that causes the events of the fixture.
func eventSender() githubfake.Sender { return githubfake.Sender{ID: 31337, Login: "octo"} }

func (x *hostFixture) Event(_ context.Context, kind forge.EventKind, secret []byte) (forge.EventRequest, error) {
	return x.deliver(x.eventOf(kind), secret), nil
}

func (x *hostFixture) RawEvent(
	_ context.Context, kind forge.EventKind, body, secret []byte,
) (forge.EventRequest, error) {
	name := x.eventOf(kind).Name

	return x.deliver(githubfake.RawEvent(name, body), secret), nil
}

func (x *hostFixture) Redeliver(_ context.Context, r forge.EventRequest) (forge.EventRequest, error) {
	id := ""
	if values := r.Header["X-Github-Delivery"]; len(values) == 1 {
		id = values[0]
	}

	x.mu.Lock()
	secret, found := x.secrets[id]
	x.mu.Unlock()

	if !found {
		return forge.EventRequest{}, fmt.Errorf("%w: no delivery %q", errNotImplemented, id)
	}

	d := x.host.Redeliver(githubfake.Hook{ID: hookID, Secret: secret}, githubfake.Delivery{
		ID: id, Header: http.Header(r.Header), Body: r.Body,
	})

	return forge.EventRequest{Header: d.Header, Body: d.Body}, nil
}

// TokenAccess asks the fake with the token: it reads the head of main, and it
// posts a branch at a commit that does not exist. A token that may write gets
// the answer 422 of the validation, and a token that may not gets another
// answer. The post changes nothing.
func (x *hostFixture) TokenAccess(ctx context.Context, token []byte, repo string) (forgetest.Access, error) {
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: x.host.CertPool(), MinVersion: tls.VersionTLS12,
	}}}
	defer client.CloseIdleConnections()

	probe := func(method, path, body string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, method, x.host.URL()+path, strings.NewReader(body))
		if err != nil {
			return 0, fmt.Errorf("build the probe: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+string(token))

		resp, err := client.Do(req)
		if err != nil {
			return 0, fmt.Errorf("send the probe: %w", err)
		}

		_, copyErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()

		if err := errors.Join(copyErr, closeErr); err != nil {
			return 0, fmt.Errorf("read the probe answer: %w", err)
		}

		return resp.StatusCode, nil
	}

	read, err := probe(http.MethodGet, "/repos/"+repo+"/commits/main", "")
	if err != nil {
		return forgetest.Access{}, err
	}

	write, err := probe(http.MethodPost, "/repos/"+repo+"/git/refs",
		`{"ref":"refs/heads/probe","sha":"`+strings.Repeat("0", 40)+`"}`)
	if err != nil {
		return forgetest.Access{}, err
	}

	return forgetest.Access{
		Read: read == http.StatusOK, Write: write == http.StatusUnprocessableEntity || write == http.StatusCreated,
	}, nil
}

func (x *hostFixture) Refuse(context.Context) error {
	x.host.Refuse()

	return nil
}

func (x *hostFixture) Calls(context.Context) (int, error) { return x.host.Calls(), nil }

func (x *hostFixture) SetTokenExpiry(_ context.Context, at time.Time) error {
	x.host.SetTokenExpiry(at)

	return nil
}

func (x *hostFixture) Garble(context.Context) (func(), error) { return x.host.Garble(), nil }

func (x *hostFixture) Unavailable(_ context.Context, retryAfter time.Duration) (func(), error) {
	return x.host.Unavailable(retryAfter), nil
}

// eventOf returns the fake event of kind. A kind that the port does not name
// gives an event of that name with an empty object.
func (x *hostFixture) eventOf(kind forge.EventKind) githubfake.Event {
	switch kind {
	case forge.EventPing:
		return x.host.Ping(githubfake.Hook{ID: hookID, Secret: nil}, eventSender())
	case forge.EventReview:
		return x.host.PullRequestEvent(githubfake.ReviewOpened, x.repo, githubfake.PullRequest{
			Number: 1,
			Head: githubfake.Commit{
				ID:        strings.Repeat("a", 40),
				Parent:    "",
				Message:   "",
				Author:    "",
				Committer: "",
				Verified:  false,
				Time:      time.Time{},
			},
			HeadBranch: "feature",
			BaseBranch: "main",
			Merged:     false,
		}, eventSender())
	}

	return githubfake.RawEvent(string(kind), []byte("{}"))
}

// remember keeps the secret of a delivery, so that Redeliver signs it again.
func (x *hostFixture) remember(id string, secret []byte) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.secrets == nil {
		x.secrets = map[string][]byte{}
	}

	x.secrets[id] = secret
}

func (x *hostFixture) deliver(ev githubfake.Event, secret []byte) forge.EventRequest {
	d := x.host.NewDelivery(githubfake.Hook{ID: hookID, Secret: secret}, ev)
	x.remember(d.ID, secret)

	return forge.EventRequest{Header: d.Header, Body: d.Body}
}

const conformanceRepo = "acme/infra"

// newHost starts a fake GitHub host with the repository of the suite, one
// commit on main, and a token for the user. The installation may write the
// contents, so that the suite can commit.
func newHost(t *testing.T) *githubfake.Fake {
	t.Helper()

	return newHostWith(t, func(c *githubfake.Config) {
		c.Permissions = map[string]string{
			"contents": "write", "metadata": "read", "pull_requests": "write", "statuses": "write", "issues": "write",
		}
	})
}

// newHostWith is newHost with a change of the settings of the host.
func newHostWith(t *testing.T, change func(*githubfake.Config)) *githubfake.Fake {
	t.Helper()

	cfg := githubfake.Config{
		AppID: "", AppSlug: "", InstallationID: "", Owner: githubfake.Account{ID: 99, Login: "acme"},
		User: githubfake.Account{ID: 0, Login: ""}, UserToken: "github_pat_" + randomHex(t, 24), ClassicToken: false,
		Permissions: nil, ContentsLimit: 0, Key: testKey(), Now: nil, WebhookClient: nil,
	}
	change(&cfg)

	host := githubfake.New(t, cfg)

	if _, err := host.AddRepo(conformanceRepo, map[string][]byte{"README.md": []byte("hello\n")}); err != nil {
		t.Fatal(err)
	}

	return host
}

// newHostOpener returns an opener that trusts the fake host and may reach
// the loopback.
func newHostOpener(t *testing.T, host *githubfake.Fake) *github.Opener {
	t.Helper()

	return newHostOpenerAt(t, host, func() time.Time { return githubfake.Epoch().Add(time.Minute) })
}

// newHostOpenerAt is newHostOpener with a clock for the adapter. The clock
// of the fake starts at its Epoch, so the clock of the adapter does too.
func newHostOpenerAt(t *testing.T, host *githubfake.Fake, now func() time.Time) *github.Opener {
	t.Helper()

	o, err := github.NewOpener(github.Config{
		Rules: egress.Rules{
			Allow:            nil,
			LoopbackForTests: true,
		},
		RootCAs: host.CertPool(),
		Now:     now,
		Prover:  testProver{}, AppBudget: nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(o.CloseIdleConnections)

	return o
}

// hostConnection returns the connection and the credential of auth for host.
func hostConnection(t *testing.T, host *githubfake.Fake, auth forge.AuthKind) (forge.Connection, forge.Credential) {
	t.Helper()

	conn := forge.Connection{
		ID: "c1", Kind: forge.KindGitHub, Auth: auth, BaseURL: host.URL(), Params: nil, Push: true,
	}
	secret := []byte(host.UserToken())

	if auth == forge.AuthApp {
		conn.Params = map[string]string{
			forge.ParamAppID: host.AppID(), forge.ParamInstallationID: host.InstallationID(),
		}
		secret = pkcs1PEM(host.PrivateKey())
	}

	return conn, forge.Credential{SetID: "s1", Secret: secret, CreatedAt: time.Now()}
}

// conformanceHarness returns a factory of harnesses over the fake GitHub
// host of package githubfake, one host for each case.
func conformanceHarness(auth forge.AuthKind) forgetest.Factory {
	return func(t *testing.T) forgetest.Harness {
		t.Helper()

		host := newHost(t)
		o := newHostOpener(t, host)
		conn, cred := hostConnection(t, host, auth)

		fg, err := o.Open(t.Context(), conn, cred)
		if err != nil {
			t.Fatalf("Open = %v", err)
		}

		account := forge.Account{ID: "777", Login: "acme", ExpiresAt: time.Time{}, NoExpiry: false, Scopes: nil}
		if auth == forge.AuthApp {
			account.ID, account.Login = "99", "acme"
		}

		return forgetest.Harness{
			Opener:      o,
			Connection:  conn,
			Credential:  cred,
			EventSecret: []byte(randomHex(t, 32)),
			Forge:       fg,
			Account:     account,
			Repo:        conformanceRepo,
			Fixture:     &hostFixture{host: host, repo: conformanceRepo, auth: auth, mu: sync.Mutex{}, secrets: nil},
			Now:         func() time.Time { return githubfake.Epoch().Add(time.Minute) },
		}
	}
}
