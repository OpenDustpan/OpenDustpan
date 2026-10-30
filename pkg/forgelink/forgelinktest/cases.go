package forgelinktest

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Sizes and times of the cases.
const (
	// racers is the number of callers in the cases with many calls at once.
	racers = 16
	// firstRacer is the account ID of the first caller of the case with many proofs.
	firstRacer = 100
	// twoLinks is the number of links in the cases with two accounts.
	twoLinks = 2
	// proveAt, acceptAt and afterAccept are the times after the start of a request
	// at which the cases prove it, accept it, and try it again.
	proveAt     = time.Minute
	acceptAt    = 2 * time.Minute
	afterAccept = 3 * time.Minute
)

func caseRequestRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	hash := start(t, h, tokenA, ana)

	got, err := h.Store.GetRequest(t.Context(), hash)
	must(t, "get", err)

	want, err := forgelink.NewRequest(tokenA, ana, at(0))
	must(t, "new request", err)

	if got != want {
		t.Errorf("request = %+v, want %+v", got, want)
	}

	if hash != forgelink.HashToken(tokenA) || strings.Contains(fmt.Sprintf("%+v", got), tokenA) {
		t.Error("the request holds the token or a hash of another token")
	}
}

func caseRequestConflict(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	start(t, h, tokenA, ana)

	r, err := forgelink.NewRequest(tokenA, ana, at(proveAt))
	must(t, "new request", err)
	wantErr(t, "same hash", h.Store.CreateRequest(t.Context(), r), forgelink.ErrConflict)
}

func caseRequestInvalid(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")

	good, err := forgelink.NewRequest(tokenA, ana, at(0))
	must(t, "new request", err)

	proved := good
	proved.Proved, proved.ProvedAt = proof("42", "ana-gh"), at(time.Second)

	used := good
	used.UsedAt = at(time.Second)

	badHash := good
	badHash.TokenSHA256 = strings.ToUpper(good.TokenSHA256)

	badLifetime := good
	badLifetime.ExpiresAt = good.CreatedAt.Add(time.Hour)

	noPrincipal := good
	noPrincipal.Principal = ""

	for name, r := range map[string]*forgelink.Request{
		"a proof": &proved, "an accept": &used, "a hash in capitals": &badHash, "a long lifetime": &badLifetime,
		"no principal": &noPrincipal,
	} {
		wantErr(t, name, h.Store.CreateRequest(t.Context(), *r), forgelink.ErrInvalid)
	}

	if _, err := forgelink.NewRequest("short", ana, at(0)); err == nil {
		t.Error("a short token made a request")
	}
}

func caseRequestLimit(t *testing.T, h Harness) {
	t.Helper()

	ana, bob := h.Principal(t, "ana"), h.Principal(t, "bob")

	for i := range forgelink.MaxOpenRequests {
		r, err := forgelink.NewRequest(fmt.Sprintf("%02d%s", i, tokenA[2:]), ana, at(time.Duration(i)*time.Second))
		must(t, "new request", err)
		must(t, "create request", h.Store.CreateRequest(t.Context(), r))
	}

	over, err := forgelink.NewRequest(tokenB, ana, at(proveAt))
	must(t, "new request", err)
	wantErr(t, "one over the limit", h.Store.CreateRequest(t.Context(), over), forgelink.ErrLimit)

	// The limit is for one principal.
	other, err := forgelink.NewRequest(tokenB, bob, at(proveAt))
	must(t, "new request", err)
	must(t, "another principal", h.Store.CreateRequest(t.Context(), other))

	// After the expiry of the open requests, the person can start again, and the
	// expired requests are gone.
	later, err := forgelink.NewRequest("tokenCtokenCtokenCtokenCtokenCtokenCtokenCtokenC", ana, at(time.Hour))
	must(t, "new request", err)
	must(t, "after the expiry", h.Store.CreateRequest(t.Context(), later))

	_, err = h.Store.GetRequest(t.Context(), forgelink.HashToken(fmt.Sprintf("%02d%s", 0, tokenA[2:])))
	wantErr(t, "an expired request", err, forgelink.ErrNotFound)
}

func caseRequestUnknown(t *testing.T, h Harness) {
	t.Helper()

	_, err := h.Store.GetRequest(t.Context(), forgelink.HashToken(tokenA))
	wantErr(t, "get", err, forgelink.ErrNotFound)

	_, err = h.Store.ProveRequest(t.Context(), forgelink.HashToken(tokenA), proof("42", "x"), at(0))
	wantErr(t, "prove", err, forgelink.ErrNotFound)

	_, _, err = accept(t, h, forgelink.HashToken(tokenA), "ana", "")
	wantErr(t, "accept", err, forgelink.ErrNotFound)
}

func caseProve(t *testing.T, h Harness) {
	t.Helper()

	hash := start(t, h, tokenA, h.Principal(t, "ana"))

	got, err := h.Store.ProveRequest(t.Context(), hash, proof("42", "ana-gh"), at(proveAt))
	must(t, "prove", err)

	if !got.IsProved() || got.Proved != proof("42", "ana-gh") || !got.ProvedAt.Equal(at(proveAt)) {
		t.Errorf("proved request = %+v", got)
	}

	stored, err := h.Store.GetRequest(t.Context(), hash)
	must(t, "get", err)

	if stored != got {
		t.Errorf("stored request = %+v, want %+v", stored, got)
	}
}

func caseProveRepeat(t *testing.T, h Harness) {
	t.Helper()

	hash := start(t, h, tokenA, h.Principal(t, "ana"))
	first, err := h.Store.ProveRequest(t.Context(), hash, proof("42", "ana-gh"), at(proveAt))
	must(t, "prove", err)

	// A Git host sends an event again. The login can differ, the account does not.
	again, err := h.Store.ProveRequest(t.Context(), hash, proof("42", "ana-renamed"), at(acceptAt))
	must(t, "prove again", err)

	if again != first {
		t.Errorf("a repeated proof changed the request: %+v, want %+v", again, first)
	}
}

func caseProveOtherAccount(t *testing.T, h Harness) {
	t.Helper()

	hash := start(t, h, tokenA, h.Principal(t, "ana"))
	prove(t, h, hash, "42", "ana-gh")

	_, err := h.Store.ProveRequest(t.Context(), hash, proof("43", "mallory"), at(acceptAt))
	wantErr(t, "second account", err, forgelink.ErrConflict)

	got, err := h.Store.GetRequest(t.Context(), hash)
	must(t, "get", err)

	if got.Proved.Account.ID != "42" {
		t.Errorf("the account of the proof is %q, want 42", got.Proved.Account.ID)
	}
}

func caseProveRefusals(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")

	expired := start(t, h, tokenA, ana)
	_, err := h.Store.ProveRequest(t.Context(), expired, proof("42", "x"), at(forgelink.RequestLifetime))
	wantErr(t, "at the expiry", err, forgelink.ErrExpired)

	used := start(t, h, tokenB, ana)
	prove(t, h, used, "42", "x")
	_, _, err = accept(t, h, used, ana, "")
	must(t, "accept", err)

	_, err = h.Store.ProveRequest(t.Context(), used, proof("42", "x"), at(afterAccept))
	wantErr(t, "used request", err, forgelink.ErrUsed)
}

func caseProveInvalid(t *testing.T, h Harness) {
	t.Helper()

	hash := start(t, h, tokenA, h.Principal(t, "ana"))

	other := proof("42", "x")
	other.Account.Kind = "svn"

	upper := proof("42", "x")
	upper.Account.Host = "GitHub.com"

	spaced := proof("4 2", "x")
	noDelivery := proof("42", "x")
	noDelivery.DeliveryID = ""

	for name, p := range map[string]forgelink.Proof{
		"unknown kind": other, "host in capitals": upper, "space in the ID": spaced, "no delivery": noDelivery,
		"no ID": proof("", "x"),
	} {
		_, err := h.Store.ProveRequest(t.Context(), hash, p, at(proveAt))
		wantErr(t, name, err, forgelink.ErrInvalid)
	}

	got, err := h.Store.GetRequest(t.Context(), hash)
	must(t, "get", err)

	if got.IsProved() {
		t.Error("an invalid proof was recorded")
	}
}

func caseAcceptNotProved(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	hash := start(t, h, tokenA, ana)

	_, _, err := accept(t, h, hash, ana, "")
	wantErr(t, "accept without a proof", err, forgelink.ErrNotProved)

	// The refusal did not use the request: a proof and a second accept work.
	prove(t, h, hash, "42", "ana-gh")

	_, _, err = accept(t, h, hash, ana, "")
	must(t, "accept after the proof", err)
}

func caseProofAloneMakesNoLink(t *testing.T, h Harness) {
	t.Helper()

	hash := start(t, h, tokenA, h.Principal(t, "ana"))
	prove(t, h, hash, "42", "ana-gh")

	_, err := h.Store.LinkOf(t.Context(), account("42"))
	wantErr(t, "link of the account", err, forgelink.ErrNotFound)

	if n := entries(t, h); n != 0 {
		t.Errorf("a proof appended %d audit entries, want none: it changes no access", n)
	}
}

func caseAcceptDenied(t *testing.T, h Harness) {
	t.Helper()

	ana, bob := h.Principal(t, "ana"), h.Principal(t, "bob")
	hash := start(t, h, tokenA, ana)
	prove(t, h, hash, "42", "ana-gh")

	_, _, err := accept(t, h, hash, bob, "")
	wantErr(t, "another principal", err, forgelink.ErrDenied)

	_, _, err = accept(t, h, hash, "", "")
	wantErr(t, "no principal", err, forgelink.ErrDenied)

	_, err = h.Store.LinkOf(t.Context(), account("42"))
	wantErr(t, "link after the refusals", err, forgelink.ErrNotFound)

	// The principal that started the request can still accept.
	_, _, err = accept(t, h, hash, ana, "")
	must(t, "accept by the starter", err)
}

func caseAccept(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	hash := start(t, h, tokenA, ana)
	prove(t, h, hash, "42", "ana-gh")

	got, replaced, err := accept(t, h, hash, ana, "")
	must(t, "accept", err)

	want := forgelink.Link{Account: account("42"), Login: "ana-gh", Principal: ana, LinkedAt: at(acceptAt)}
	if got != want || replaced != "" {
		t.Errorf("link = %+v, replaced %q, want %+v", got, replaced, want)
	}

	stored, err := h.Store.LinkOf(t.Context(), account("42"))
	must(t, "link of", err)

	if stored != want {
		t.Errorf("stored link = %+v, want %+v", stored, want)
	}
}

func caseAcceptOnce(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	hash := start(t, h, tokenA, ana)
	prove(t, h, hash, "42", "ana-gh")

	_, _, err := accept(t, h, hash, ana, "")
	must(t, "accept", err)

	_, _, err = accept(t, h, hash, ana, "")
	wantErr(t, "second accept", err, forgelink.ErrUsed)

	if n := entries(t, h); n != 1 {
		t.Errorf("audit entries = %d, want 1", n)
	}
}

func caseAcceptExpired(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	hash := start(t, h, tokenA, ana)
	prove(t, h, hash, "42", "ana-gh")

	_, err := h.Store.AcceptRequest(t.Context(), acceptance(hash, ana, "", at(forgelink.RequestLifetime)), linkEntry())
	wantErr(t, "at the expiry", err, forgelink.ErrExpired)

	_, err = h.Store.LinkOf(t.Context(), account("42"))
	wantErr(t, "link", err, forgelink.ErrNotFound)
}

func caseNoSilentReplace(t *testing.T, h Harness) {
	t.Helper()

	ana, bob := h.Principal(t, "ana"), h.Principal(t, "bob")
	link(t, h, tokenA, ana, "42")

	// Bob links the same account. The accept page showed no link (previous empty),
	// but the account has one: the write is a conflict, not a replace.
	hash := start(t, h, tokenB, bob)
	prove(t, h, hash, "42", "ana-gh")

	_, _, err := accept(t, h, hash, bob, "")
	wantErr(t, "no previous", err, forgelink.ErrConflict)

	_, _, err = accept(t, h, hash, bob, bob)
	wantErr(t, "previous names another principal", err, forgelink.ErrConflict)

	stored, err := h.Store.LinkOf(t.Context(), account("42"))
	must(t, "link of", err)

	if stored.Principal != ana {
		t.Errorf("the link names %q, want %q: a refused accept replaced it", stored.Principal, ana)
	}

	if n := entries(t, h); n != 1 {
		t.Errorf("audit entries = %d, want 1: a refused accept appended one", n)
	}
}

func caseReplace(t *testing.T, h Harness) {
	t.Helper()

	ana, bob := h.Principal(t, "ana"), h.Principal(t, "bob")
	link(t, h, tokenA, ana, "42")

	hash := start(t, h, tokenB, bob)
	prove(t, h, hash, "42", "ana-gh")

	got, replaced, err := accept(t, h, hash, bob, ana)
	must(t, "accept with the previous principal", err)

	if got.Principal != bob || replaced != ana {
		t.Errorf("link %+v, replaced %q, want a link of %q that replaced %q", got, replaced, bob, ana)
	}

	// A previous link that is not there is a conflict too.
	other := start(t, h, "tokenCtokenCtokenCtokenCtokenCtokenCtokenCtokenC", bob)
	prove(t, h, other, "77", "x")

	_, _, err = accept(t, h, other, bob, ana)
	wantErr(t, "previous link missing", err, forgelink.ErrConflict)
}

func caseRefusedAcceptKeepsRequest(t *testing.T, h Harness) {
	t.Helper()

	ana, bob := h.Principal(t, "ana"), h.Principal(t, "bob")
	link(t, h, tokenA, ana, "42")

	hash := start(t, h, tokenB, bob)
	prove(t, h, hash, "42", "x")

	_, _, err := accept(t, h, hash, bob, "")
	wantErr(t, "conflict", err, forgelink.ErrConflict)

	got, err := h.Store.GetRequest(t.Context(), hash)
	must(t, "get", err)

	if !got.UsedAt.IsZero() {
		t.Error("a refused accept used the request: the person cannot try again with the right previous principal")
	}

	_, _, err = accept(t, h, hash, bob, ana)
	must(t, "accept with the previous principal", err)
}

func caseExactIDs(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	link(t, h, tokenA, ana, "AbC")

	for name, a := range map[string]forgelink.Account{
		"ID in lowercase":  account("abc"),
		"ID with a suffix": account("AbC1"),
		"other host":       {Kind: forge.KindGitHub, Host: "ghe.example.com", ID: "AbC"},
		"other kind":       {Kind: forge.KindGitLab, Host: host, ID: "AbC"},
	} {
		_, err := h.Store.LinkOf(t.Context(), a)
		wantErr(t, name, err, forgelink.ErrNotFound)
	}

	if _, err := h.Store.LinkOf(t.Context(), account("AbC")); err != nil {
		t.Errorf("link of the exact account: %v", err)
	}

	// An ID with other capitals is another account and makes its own link.
	link(t, h, tokenB, ana, "abc")

	if links, err := h.Store.LinksOf(t.Context(), ana); err != nil || len(links) != twoLinks {
		t.Errorf("links = %v, %v, want two accounts", links, err)
	}
}

func caseRenamedLogin(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	link(t, h, tokenA, ana, "42")

	// The account renames its login to the old login of another account.
	stored, err := h.Store.LinkOf(t.Context(), account("42"))
	must(t, "link of", err)

	if stored.Login != "login-42" || stored.Account.ID != "42" {
		t.Errorf("link = %+v", stored)
	}

	// A different account with the login of the first one makes no match.
	other := forgelink.Account{Kind: forge.KindGitHub, Host: host, ID: "login-42"}
	if _, otherErr := h.Store.LinkOf(t.Context(), other); otherErr == nil {
		t.Error("a login matched an account")
	}

	// The same account links again under its new login: the same row, the new text.
	hash := start(t, h, tokenB, ana)
	prove(t, h, hash, "42", "renamed")

	got, _, err := accept(t, h, hash, ana, ana)
	must(t, "accept", err)

	if got.Login != "renamed" || got.Account.ID != "42" {
		t.Errorf("link = %+v, want the login renamed", got)
	}
}

func caseConcurrentAccept(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	hash := start(t, h, tokenA, ana)
	prove(t, h, hash, "42", "ana-gh")

	results := make([]error, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() {
			_, _, results[i] = accept(t, h, hash, ana, "")
		})
	}

	wg.Wait()

	wins := 0

	for _, err := range results {
		if err == nil {
			wins++

			continue
		}

		wantErr(t, "a loser", err, forgelink.ErrUsed)
	}

	if wins != 1 || entries(t, h) != 1 {
		t.Errorf("%d accepts won and %d entries were appended, want 1 and 1", wins, entries(t, h))
	}
}

func caseConcurrentProve(t *testing.T, h Harness) {
	t.Helper()

	hash := start(t, h, tokenA, h.Principal(t, "ana"))
	results := make([]error, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() {
			_, results[i] = h.Store.ProveRequest(t.Context(), hash, proof(strconv.Itoa(firstRacer+i), "x"), at(proveAt))
		})
	}

	wg.Wait()

	wins := 0

	for _, err := range results {
		if err == nil {
			wins++

			continue
		}

		wantErr(t, "a loser", err, forgelink.ErrConflict)
	}

	if wins != 1 {
		t.Errorf("%d proofs won, want 1", wins)
	}
}

func caseLinksOf(t *testing.T, h Harness) {
	t.Helper()

	ana, bob := h.Principal(t, "ana"), h.Principal(t, "bob")

	link(t, h, tokenA, ana, "9")
	link(t, h, tokenB, ana, "10")
	link(t, h, "tokenCtokenCtokenCtokenCtokenCtokenCtokenCtokenC", bob, "11")

	got, err := h.Store.LinksOf(t.Context(), ana)
	must(t, "links of ana", err)

	if len(got) != 2 || got[0].Account.ID != "10" || got[1].Account.ID != "9" {
		t.Errorf("links of ana = %+v, want the IDs 10 and 9 in text order", got)
	}

	none, err := h.Store.LinksOf(t.Context(), h.Principal(t, "cy"))
	must(t, "links of cy", err)

	if len(none) != 0 {
		t.Errorf("links of a principal with none = %v", none)
	}
}

func caseDelete(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	link(t, h, tokenA, ana, "42")

	must(t, "delete", h.Store.DeleteLink(t.Context(), account("42"), entry("caller:manage-git-host-links")))

	_, err := h.Store.LinkOf(t.Context(), account("42"))
	wantErr(t, "link after the delete", err, forgelink.ErrNotFound)

	if n := entries(t, h); n != twoLinks {
		t.Errorf("audit entries = %d, want 2 (the link and the delete)", n)
	}

	var none forgelink.Account

	wantErr(t, "invalid account", h.Store.DeleteLink(t.Context(), none, linkEntry()), forgelink.ErrInvalid)
}

func caseDeleteMissing(t *testing.T, h Harness) {
	t.Helper()

	wantErr(t, "delete", h.Store.DeleteLink(t.Context(), account("42"), linkEntry()), forgelink.ErrNotFound)

	if n := entries(t, h); n != 0 {
		t.Errorf("a delete of nothing appended %d entries", n)
	}
}

func caseRelinkTime(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	link(t, h, tokenA, ana, "42")
	must(t, "delete", h.Store.DeleteLink(t.Context(), account("42"), linkEntry()))

	later := at(time.Hour)

	r, err := forgelink.NewRequest(tokenB, ana, later)
	must(t, "new request", err)
	must(t, "create request", h.Store.CreateRequest(t.Context(), r))

	_, err = h.Store.ProveRequest(t.Context(), r.TokenSHA256, proof("42", "x"), later)
	must(t, "prove", err)

	accepted, err := h.Store.AcceptRequest(t.Context(), acceptance(r.TokenSHA256, ana, "", later), linkEntry())
	must(t, "accept", err)

	if !accepted.Link.LinkedAt.Equal(later) {
		t.Errorf("linked at %v, want the time of the new link %v", accepted.Link.LinkedAt, later)
	}
}

func caseEntryScope(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	hash := start(t, h, tokenA, ana)
	prove(t, h, hash, "42", "ana-gh")

	foreign := linkEntry()
	foreign.Namespace = "shop"

	empty := linkEntry()
	empty.Action = ""

	for name, e := range map[string]*audit.Entry{"another scope": &foreign, "invalid entry": &empty} {
		_, err := h.Store.AcceptRequest(t.Context(), acceptance(hash, ana, "", at(acceptAt)), *e)
		wantErr(t, name, err, audit.ErrInvalidEntry)
	}

	got, err := h.Store.GetRequest(t.Context(), hash)
	must(t, "get", err)

	if !got.UsedAt.IsZero() {
		t.Error("a write with a bad entry used the request")
	}

	if _, err := h.Store.LinkOf(t.Context(), account("42")); err == nil {
		t.Error("a write with a bad entry made a link")
	}

	linkAndDelete := h.Store.DeleteLink(t.Context(), account("42"), foreign)
	if linkAndDelete == nil {
		t.Error("a delete with an entry of another scope succeeded")
	}
}

func caseAcceptAudit(t *testing.T, h Harness) {
	t.Helper()

	ana := h.Principal(t, "ana")
	hash := start(t, h, tokenA, ana)
	prove(t, h, hash, "42", "ana-gh")

	e := linkEntry()
	e.Reason = "test"

	_, err := h.Store.AcceptRequest(t.Context(), acceptance(hash, ana, "", at(acceptAt)), e)
	must(t, "accept", err)

	got := h.AuditEntries(t)
	if len(got) != 1 || got[0].Namespace != audit.ServerScope || got[0].Action != e.Action {
		t.Errorf("audit entries = %+v, want one entry of %q in the server scope", got, e.Action)
	}
}

func caseReplaceEntry(t *testing.T, h Harness) {
	t.Helper()

	ana, bob := h.Principal(t, "ana"), h.Principal(t, "bob")
	link(t, h, tokenA, ana, "42")

	hash := start(t, h, tokenB, bob)
	prove(t, h, hash, "42", "ana-gh")

	before := entries(t, h)

	_, _, err := accept(t, h, hash, bob, ana)
	must(t, "accept", err)

	got := h.AuditEntries(t)
	if len(got) != before+twoLinks {
		t.Fatalf("audit entries = %d, want %d: the link and the loss of the old holder", len(got), before+twoLinks)
	}

	// The order of the two entries is not part of the contract: the old holder
	// finds exactly one of them under its own object.
	under := 0

	for _, e := range got[before:] {
		if e.Object == principal.Ref(ana) {
			under++
		}
	}

	if under != 1 {
		t.Errorf("%d of the new entries are under %q, want the entry of the loss only", under, principal.Ref(ana))
	}
}

func caseReplaceBadEntry(t *testing.T, h Harness) {
	t.Helper()

	ana, bob := h.Principal(t, "ana"), h.Principal(t, "bob")
	link(t, h, tokenA, ana, "42")

	hash := start(t, h, tokenB, bob)
	prove(t, h, hash, "42", "ana-gh")

	a := acceptance(hash, bob, ana, at(acceptAt))
	a.Lost.Action = ""

	before := entries(t, h)

	_, err := h.Store.AcceptRequest(t.Context(), a, linkEntry())
	wantErr(t, "invalid entry of the old holder", err, audit.ErrInvalidEntry)

	a.Lost = linkEntry()
	a.Lost.Namespace = "shop"

	_, err = h.Store.AcceptRequest(t.Context(), a, linkEntry())
	wantErr(t, "entry of another scope", err, audit.ErrInvalidEntry)

	stored, err := h.Store.LinkOf(t.Context(), account("42"))
	must(t, "link of", err)

	req, getErr := h.Store.GetRequest(t.Context(), hash)
	must(t, "get", getErr)

	if stored.Principal != ana || !req.UsedAt.IsZero() || entries(t, h) != before {
		t.Errorf("a refused replace changed the link %+v, the request %+v or the entries", stored, req)
	}
}
