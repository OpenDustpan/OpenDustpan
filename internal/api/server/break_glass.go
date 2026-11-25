package server

// Break-glass account routes of the installation API. A break-glass account
// is a local person that a site administrator can use when every other
// sign-in path fails, so the routes that change one need a fresh
// authentication and name the object @server:
//
//   - server:read-principals lists the accounts and the live count,
//   - server:manage-principals adds an account with its first passkey and
//     rotates the passkey of an account.
//
// The list route reads the working count of the store at the request time:
// an account that expires by time leaves the count without a write. The add
// and the rotate write their audit entries in the store transaction; the
// passkey service adds the entry of its own change. The store holds one
// transaction for a rotation, so no commit ends the old passkey before the
// new one is stored.
//
// A break-glass account without a passkey is not a working path. The add
// stores the account first and then its passkey: a failure of the passkey
// write leaves an account with no credential, and the list route reports it,
// so an administrator can delete it and add another. The count never treats
// it as working.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Route paths and the description tag of the break-glass accounts.
const (
	breakGlassPath        = "/api/dustpan/v1/break-glass/accounts"
	breakGlassAccountPath = "/api/dustpan/v1/break-glass/accounts/{id}"
	tagBreakGlass         = "break-glass"
)

// maxBreakGlassBodyBytes bounds a break-glass body. A credential ID has at
// most 1023 bytes and a public key at most 4096 bytes, and the JSON carries
// them in base64, so this is ample.
const maxBreakGlassBodyBytes = 16 * 1024

// errBreakGlassBody reports a body that is not the one of a break-glass
// add or rotation.
var errBreakGlassBody = errors.New(
	`invalid break-glass body: provide {"subject":"...","name":"...","credential":"...","publicKey":"..."} ` +
		`or {"oldPasskey":"pky-...","name":"...","credential":"...","publicKey":"..."}`,
)

// errBreakGlassNotFound is the answer of a rotation of an ID that names no
// break-glass account, and of a passkey that the account does not hold.
var errBreakGlassNotFound = &codedError{
	status: http.StatusNotFound, code: errcode.BreakGlassNotFound, args: nil,
	message: "No break-glass account has this ID, or the passkey is not one of its records.",
}

// breakGlassBusyMessage is the answer of a guarded change that waited for
// the break-glass lock past its timeout.
const breakGlassBusyMessage = "Another change holds the break-glass lock. " +
	"The server wrote nothing. Retry the request."

// breakGlassAccountResponse is one break-glass account on the wire. Enabled
// is false after a disable, and a null disabledAt means that the account is
// enabled. The working count of the installation is on the list response,
// not on each account: it also depends on the grants and the passkeys of the
// account.
//
// Contract: never remove or rename these fields.
type breakGlassAccountResponse struct {
	ID          string        `json:"id"`
	Subject     string        `json:"subject"`
	DisplayName string        `json:"displayName"`
	Enabled     bool          `json:"enabled"`
	CreatedAt   string        `json:"createdAt"`
	DisabledAt  *string       `json:"disabledAt"`
	Passkeys    []passkeyBody `json:"passkeys"`
}

// breakGlassListResponse is the list of the break-glass accounts with the
// live count. Working is the count of the accounts that the floor rule
// counts at the request time. Missing is the count that the installation
// needs to reach the floor, 0 at or above it.
//
// Contract: never remove or rename these fields.
type breakGlassListResponse struct {
	Accounts []breakGlassAccountResponse `json:"accounts"`
	Working  int64                       `json:"working"`
	Missing  int64                       `json:"missing"`
	Floor    int                         `json:"floor"`
}

// addBreakGlassRequest is the body of a break-glass add: the person and the
// first passkey of the account. CredentialID and PublicKey are base64url
// without padding. CreatedAt and the passkey ID come from the server.
type addBreakGlassRequest struct {
	Subject        string   `json:"subject"`
	DisplayName    string   `json:"displayName"`
	Name           string   `json:"name"`
	Credential     string   `json:"credential"`
	PublicKey      string   `json:"publicKey"`
	Transports     []string `json:"transports"`
	SignCount      uint32   `json:"signCount"`
	BackupEligible bool     `json:"backupEligible"`
}

// passkey returns the passkey fields of the body.
func (r addBreakGlassRequest) passkey() breakGlassPasskey {
	return breakGlassPasskey{
		Name: r.Name, CredentialID: r.Credential, PublicKey: r.PublicKey,
		Transports: r.Transports, SignCount: r.SignCount, BackupEligible: r.BackupEligible,
	}
}

// rotateBreakGlassRequest is the body of a break-glass rotation: the passkey
// that ends and the passkey that replaces it.
type rotateBreakGlassRequest struct {
	OldPasskey     string   `json:"oldPasskey"`
	Name           string   `json:"name"`
	Credential     string   `json:"credential"`
	PublicKey      string   `json:"publicKey"`
	Transports     []string `json:"transports"`
	SignCount      uint32   `json:"signCount"`
	BackupEligible bool     `json:"backupEligible"`
}

// passkey returns the passkey fields of the body.
func (r rotateBreakGlassRequest) passkey() breakGlassPasskey {
	return breakGlassPasskey{
		Name: r.Name, CredentialID: r.Credential, PublicKey: r.PublicKey,
		Transports: r.Transports, SignCount: r.SignCount, BackupEligible: r.BackupEligible,
	}
}

// breakGlassPasskey names the fields of a passkey in a break-glass body.
type breakGlassPasskey struct {
	Name           string
	CredentialID   string
	PublicKey      string
	Transports     []string
	SignCount      uint32
	BackupEligible bool
}

// breakGlassRoutes returns the break-glass rows of the route table. A
// handler with no principal service or no passkey service mounts none.
func (h *Handler) breakGlassRoutes() []Route {
	if h.principals == nil || h.passkeys == nil || h.signIn == nil {
		return nil
	}

	read := []identity.Action{identity.ActionServerReadPrincipals}
	manage := []identity.Action{identity.ActionServerManagePrincipals}

	return []Route{
		authorizedRoute(http.MethodGet, breakGlassPath, read, resolveServer, h.listBreakGlass),
		authorizedRoute(http.MethodPost, breakGlassPath, manage, resolveServer,
			h.fresh(identity.ActionServerManagePrincipals, h.addBreakGlass)),
		authorizedRoute(http.MethodPost, breakGlassAccountPath+"/rotate", manage, resolveServer,
			h.fresh(identity.ActionServerManagePrincipals, h.rotateBreakGlass)),
	}
}

// listBreakGlass serves GET /api/dustpan/v1/break-glass/accounts: the accounts,
// each with its passkeys, and the live working and missing counts. The count
// is the one of the store at the request time, so a change of a passkey or a
// grant shows in the next list without a write to the account.
func (h *Handler) listBreakGlass(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	accounts, err := h.principals.ListBreakGlassAccounts(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	working, err := h.principals.CountWorkingBreakGlass(ctx, h.now())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := breakGlassListResponse{
		Accounts: make([]breakGlassAccountResponse, 0, len(accounts)),
		Working:  working,
		Missing:  0,
		Floor:    principal.FloorWorkingPaths,
	}

	if working < principal.FloorWorkingPaths {
		out.Missing = int64(principal.FloorWorkingPaths) - working
	}

	entry := callerEntry(r, p, identity.ActionServerReadPrincipals)

	for i := range accounts {
		account := &accounts[i]
		keys, keyErr := h.passkeys.Passkeys(ctx, mfasvc.Actor{
			Entry: entry, PrincipalID: account.ID, SessionID: "",
		})
		if keyErr != nil {
			h.writeError(ctx, w, keyErr)

			return
		}

		out.Accounts = append(out.Accounts, breakGlassAccountOf(*account, keys))
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// addBreakGlass serves POST /api/dustpan/v1/break-glass/accounts: it stores a
// new break-glass person and its first passkey, and answers 201 with the
// account. It accepts the add whatever the current count. A stale fresh
// step answers 403 before this handler runs.
func (h *Handler) addBreakGlass(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	var req addBreakGlassRequest
	if err := readBreakGlassBody(w, r, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	key, err := passkeyOf(req.passkey())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entry := callerEntry(r, p, identity.ActionServerManagePrincipals)

	account, err := h.principals.CreateBreakGlassAccount(ctx, entry, principal.Input{
		Subject: req.Subject, DisplayName: req.DisplayName, Kind: principal.KindPerson,
	})
	if err != nil {
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: "break-glass",
			outcome: audit.OutcomeFailure,
		}, refusalReason(err))
		h.writeError(ctx, w, err)

		return
	}

	stored, err := h.passkeys.AddPasskey(ctx, breakGlassActor(r, entry), account.ID, key)
	if err != nil {
		h.writeError(ctx, w, passkeyError(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, breakGlassAccountOf(account, []mfa.Passkey{stored}))
}

// rotateBreakGlass serves POST /api/dustpan/v1/break-glass/accounts/{id}/rotate:
// it replaces one passkey of the account with a new one in one transaction,
// and answers 204. An ID that names no break-glass account, or a passkey
// that the account does not hold, answers 404 with break-glass.not-found. A
// stale fresh step answers 403 before this handler runs.
func (h *Handler) rotateBreakGlass(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()
	id := r.PathValue("id")

	var req rotateBreakGlassRequest
	if err := readBreakGlassBody(w, r, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	account, err := h.breakGlassAccount(ctx, id)
	if err != nil {
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: principal.Ref(id),
			outcome: audit.OutcomeDenied,
		}, "the ID names no break-glass account")
		h.writeError(ctx, w, err)

		return
	}

	key, err := passkeyOf(req.passkey())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entry := callerEntry(r, p, identity.ActionServerManagePrincipals)

	err = h.passkeys.RotatePasskey(ctx, breakGlassActor(r, entry), account.ID, req.OldPasskey, key)
	if err != nil {
		if errors.Is(err, mfa.ErrNotFound) {
			err = errBreakGlassNotFound
		} else {
			err = passkeyError(err)
		}

		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// breakGlassActor returns the actor of a break-glass write: the console
// session of the caller and the audit entry of the request. The fresh check
// has refused a request with no session before the handler runs. A request
// that reaches this function without a session keeps an empty principal, so
// the passkey service refuses it and writes nothing.
func breakGlassActor(r *http.Request, e audit.Entry) mfasvc.Actor {
	caller, ok := sessionFromRequest(r)
	if !ok {
		return mfasvc.Actor{Entry: e, PrincipalID: "", SessionID: ""}
	}

	return mfasvc.Actor{Entry: e, PrincipalID: caller.Match.Owner.ID, SessionID: caller.Match.Session.ID}
}

// breakGlassAccount reads the principal id and refuses one that is not a
// break-glass account of the installation. It answers errBreakGlassNotFound
// for an unknown record, and the store error for a read that failed.
func (h *Handler) breakGlassAccount(ctx context.Context, id string) (principal.Principal, error) {
	account, err := h.principals.GetPrincipal(ctx, id)
	if err != nil {
		if errors.Is(err, principal.ErrNotFound) {
			return principal.Principal{}, errBreakGlassNotFound
		}

		return principal.Principal{}, err
	}

	// Defense in depth: the store counts only a person of the installation
	// with the break-glass flag. This check stays so that a route that names
	// another record never reaches the rotation of the passkey store.
	if !account.BreakGlass || account.Kind != principal.KindPerson || account.Namespace != "" {
		return principal.Principal{}, errBreakGlassNotFound
	}

	return account, nil
}

// readBreakGlassBody reads the JSON body of a break-glass request under the
// limit of the route.
func readBreakGlassBody(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := readBodyUpTo(w, r, maxBreakGlassBodyBytes, errBreakGlassBody)
	if err != nil {
		return err
	}

	return decodeBody(body, dst, errBreakGlassBody)
}

// passkeyOf returns the passkey of the fields of a break-glass body. The
// service sets its ID, its person and its times. A field outside its limits,
// a transport outside the allowlist, or a value that is not base64url
// without padding returns mfa.ErrInvalid.
func passkeyOf(in breakGlassPasskey) (mfa.Passkey, error) {
	if err := mfa.ValidatePasskeyName(in.Name); err != nil {
		return mfa.Passkey{}, err
	}

	credential, err := decodePasskeyValue("credential", in.CredentialID, mfa.MaxCredentialIDBytes)
	if err != nil {
		return mfa.Passkey{}, err
	}

	key, err := decodePasskeyValue("publicKey", in.PublicKey, mfa.MaxPublicKeyBytes)
	if err != nil {
		return mfa.Passkey{}, err
	}

	if err := checkTransports(in.Transports); err != nil {
		return mfa.Passkey{}, err
	}

	return mfa.Passkey{
		CreatedAt: time.Time{}, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "", PersonID: "", Name: in.Name, CredentialID: credential, PublicKey: key,
		Transports: in.Transports, AAGUID: [16]byte{}, SignCount: in.SignCount, BackupEligible: in.BackupEligible,
	}, nil
}

// checkTransports refuses a transport outside the allowlist or one that the
// body names twice.
func checkTransports(transports []string) error {
	for i, transport := range transports {
		if !mfa.ValidTransport(transport) {
			return fmt.Errorf("%w: transport %q is not on the allowlist", mfa.ErrInvalid, transport)
		}

		if slices.Contains(transports[:i], transport) {
			return fmt.Errorf("%w: transport %q is named twice", mfa.ErrInvalid, transport)
		}
	}

	return nil
}

// decodePasskeyValue decodes one base64url value of a break-glass body and
// checks its length.
func decodePasskeyValue(field, value string, maxLen int) ([]byte, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not base64url without padding", mfa.ErrInvalid, field)
	}

	if len(b) == 0 || len(b) > maxLen {
		return nil, fmt.Errorf("%w: %s has %d bytes, want 1 to %d", mfa.ErrInvalid, field, len(b), maxLen)
	}

	return b, nil
}

// breakGlassAccountOf returns the wire form of account with its passkeys.
func breakGlassAccountOf(account principal.Principal, keys []mfa.Passkey) breakGlassAccountResponse {
	out := breakGlassAccountResponse{
		ID: account.ID, Subject: account.Subject, DisplayName: account.DisplayName,
		Enabled:   account.DisabledAt.IsZero(),
		CreatedAt: wireTime(account.CreatedAt), DisabledAt: nil,
		Passkeys: make([]passkeyBody, 0, len(keys)),
	}

	if !account.DisabledAt.IsZero() {
		at := wireTime(account.DisabledAt)
		out.DisabledAt = &at
	}

	for i := range keys {
		out.Passkeys = append(out.Passkeys, passkeyBodyOf(keys[i]))
	}

	return out
}
