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
// The list route reads the working count of the store at the request time.
// The add and the rotate write their audit entries in the store transaction;
// the passkey service adds the entry of its own change. The store holds one
// transaction for a rotation, so no commit ends the old passkey before the
// new one is stored.
//
// A break-glass account without a passkey is not a working path. The add
// makes one AddBreakGlassPerson call that stores the account and its first
// passkey in one transaction, so a failed add stores nothing. The count
// includes only an account with a passkey and a grant.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/ident"
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

// maxBreakGlassIDLength is the most characters that the ID of a principal
// has. It is the limit that the principal package uses for its own IDs.
const maxBreakGlassIDLength = 64

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
			h.breakGlassFresh(identity.ActionServerManagePrincipals, h.addBreakGlass)),
		authorizedRoute(http.MethodPost, breakGlassAccountPath+"/rotate", manage, resolveServer,
			h.breakGlassFresh(identity.ActionServerManagePrincipals, h.rotateBreakGlass)),
	}
}

// breakGlassFresh wraps a break-glass write as h.fresh does: the check runs
// for each write, and a refusal writes one failure entry for action on the
// object. The refusal of a console session with an old sign-in also names the
// enrollment path when the person holds no passkey; see breakGlassRefusal.
func (h *Handler) breakGlassFresh(action identity.Action, serve RouteHandler) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		ctx := context.WithValue(r.Context(), freshnessKey{}, h.checkTokenFreshness)

		r = r.WithContext(ctx)

		// Defense in depth: the handlers behind this wrapper do not check the
		// session themselves. This check runs for each write, so a new route
		// cannot answer a token by default.
		if err := requireFresh(r); err != nil {
			h.appendObjectChange(r, p, obj, serverChange{
				at: h.now(), action: action, object: obj.String(), outcome: audit.OutcomeFailure,
			})
			h.breakGlassRefusal(ctx, w, r, p, err)

			return
		}

		serve(w, r, p, obj)
	}
}

// breakGlassRefusal answers a refused fresh check of a break-glass write. A
// console session whose sign-in is too old, and whose person holds no passkey,
// gets the same code and the same path of a new sign-in as h.fresh gives, and
// a message that also names the path that enrolls a passkey. A person with a
// passkey, an access token and every other refusal get the answer of
// writeError.
func (h *Handler) breakGlassRefusal(
	ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal, err error,
) {
	stale, isStale := errors.AsType[reauthenticateError](err)
	caller, hasSession := sessionFromRequest(r)

	if !isStale || !hasSession {
		h.writeError(ctx, w, err)

		return
	}

	keys, keyErr := h.passkeys.Passkeys(ctx, mfasvc.Actor{
		Entry:       callerEntry(r, p, identity.ActionServerManagePrincipals),
		PrincipalID: caller.Match.Owner.ID,
		SessionID:   "",
	})
	if keyErr != nil {
		h.writeError(ctx, w, keyErr)

		return
	}

	if len(keys) > 0 {
		h.writeError(ctx, w, err)

		return
	}

	answer := problem.New(http.StatusForbidden, freshStepRequiredCode, fmt.Sprintf(
		"%s. This person has no enrolled passkey, so enroll one first at POST %s.", stale.Error(), passkeyBeginPath,
	))
	answer.Reauthenticate = stale.url

	problemWriter(h.logger).WriteProblem(ctx, w, answer)
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
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: tagBreakGlass,
			outcome: audit.OutcomeDenied,
		}, err.Error())
		h.writeError(ctx, w, err)

		return
	}

	// Allowlist check at the boundary. Defense in depth: the principal
	// service validates the subject of the person too; this check stays so
	// that a hostile subject never reaches the service, and it does not rely
	// on it.
	if err := principal.ValidateSubject(req.Subject); err != nil {
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: tagBreakGlass,
			outcome: audit.OutcomeDenied,
		}, "the subject is not valid")
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the principal service validates the display name too;
	// this check stays so that a hostile display name never reaches the
	// service, and it does not rely on it.
	if err := checkBreakGlassDisplayName(req.DisplayName); err != nil {
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: tagBreakGlass,
			outcome: audit.OutcomeDenied,
		}, "the display name is not valid")
		h.writeError(ctx, w, err)

		return
	}

	key, err := passkeyOf(req.passkey())
	if err != nil {
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: tagBreakGlass,
			outcome: audit.OutcomeDenied,
		}, err.Error())
		h.writeError(ctx, w, passkeyError(err))

		return
	}

	entry := callerEntry(r, p, identity.ActionServerManagePrincipals)

	// One call stores the person and its passkey in one transaction, so a
	// failed passkey write leaves no person and needs no compensating delete.
	account, stored, err := h.passkeys.AddBreakGlassPerson(ctx, breakGlassActor(r, entry), principal.Input{
		Subject: req.Subject, DisplayName: req.DisplayName, Kind: principal.KindPerson,
	}, key)
	if err != nil {
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: tagBreakGlass,
			outcome: audit.OutcomeFailure,
		}, refusalReason(err))
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
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: tagBreakGlass,
			outcome: audit.OutcomeDenied,
		}, err.Error())
		h.writeError(ctx, w, err)

		return
	}

	// The audit entry names the route object, not the hostile text.
	if reason := rotateIDRefusal(id, req.OldPasskey); reason != "" {
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: tagBreakGlass,
			outcome: audit.OutcomeDenied,
		}, reason)
		h.writeError(ctx, w, errBreakGlassNotFound)

		return
	}

	account, err := h.breakGlassAccount(ctx, id)
	if err != nil {
		reason := "the ID names no break-glass account"
		if !errors.Is(err, errBreakGlassNotFound) {
			reason = "the read of the principal failed"
		}

		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: principal.Ref(id),
			outcome: audit.OutcomeDenied,
		}, reason)
		h.writeError(ctx, w, err)

		return
	}

	key, err := passkeyOf(req.passkey())
	if err != nil {
		h.appendServerChangeReason(r, p, serverChange{
			at: h.now(), action: identity.ActionServerManagePrincipals, object: tagBreakGlass,
			outcome: audit.OutcomeDenied,
		}, err.Error())
		h.writeError(ctx, w, passkeyError(err))

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

// rotateIDRefusal is the allowlist check at the boundary of the IDs of a
// rotation. It returns the reason of the refusal, or "" when the account ID
// of the path and the old passkey ID of the body are both valid. An ID of
// another shape names no break-glass account or no passkey, so nothing
// reaches a service.
func rotateIDRefusal(id, oldPasskey string) string {
	// Defense in depth: the store reads by placeholder and
	// breakGlassAccount refuses an unknown record; this check stays so that
	// a hostile ID never reaches either layer, and it does not rely on them.
	if !ident.Valid(id, maxBreakGlassIDLength) {
		return "the ID has a form that no break-glass account has"
	}

	// Defense in depth: the passkey service checks the ID with ident.Valid
	// and the store reads it by placeholder; this check stays so that a
	// hostile ID never reaches either layer, and it does not rely on them.
	if !ident.Valid(oldPasskey, mfa.MaxIDLength) {
		return "the old passkey ID has a form that no passkey has"
	}

	return ""
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

// maxBreakGlassDisplayNameBytes is the length limit of a display name.
const maxBreakGlassDisplayNameBytes = 255

// breakGlassDisplayNamePunct is the punctuation that a display name can hold
// in addition to letters, marks, digits and single spaces.
const breakGlassDisplayNamePunct = ".,'-_()&@+:#/"

// checkBreakGlassDisplayName refuses a display name that is too long, is not
// UTF-8, starts or ends with white space, holds two spaces in sequence, or
// holds a character outside the allowlist: letters, marks and digits of any
// script, the space U+0020 and breakGlassDisplayNamePunct. An empty display
// name is valid. Each refusal wraps principal.ErrInvalid.
func checkBreakGlassDisplayName(s string) error {
	switch {
	case len(s) > maxBreakGlassDisplayNameBytes:
		return fmt.Errorf("%w: display name longer than %d bytes", principal.ErrInvalid, maxBreakGlassDisplayNameBytes)
	case !utf8.ValidString(s):
		return fmt.Errorf("%w: display name is not UTF-8", principal.ErrInvalid)
	case strings.TrimSpace(s) != s:
		return fmt.Errorf("%w: display name starts or ends with white space", principal.ErrInvalid)
	case strings.Contains(s, "  "):
		return fmt.Errorf("%w: display name holds two spaces in sequence", principal.ErrInvalid)
	}

	// Defense in depth: the allowlist below refuses each hidden character
	// too; this check stays so that a wider allowlist cannot let one in.
	if err := refuseHidden("display name", s); err != nil {
		return fmt.Errorf("%w: %w", principal.ErrInvalid, err)
	}

	for _, r := range s {
		if !breakGlassDisplayNameRune(r) {
			return fmt.Errorf(
				"%w: display name holds the character %U, which is not a letter, a digit, a space or one of %q",
				principal.ErrInvalid, r, breakGlassDisplayNamePunct,
			)
		}
	}

	return nil
}

// breakGlassDisplayNameRune reports whether r is in the allowlist of a
// display name.
func breakGlassDisplayNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || r == ' ' ||
		strings.ContainsRune(breakGlassDisplayNamePunct, r)
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
