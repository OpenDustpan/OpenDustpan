// Package gowebauthn is the adapter of the port mfa.RelyingParty on the
// module github.com/go-webauthn/webauthn. It makes the options of each
// ceremony and checks each response against the relying party ID and the
// origin of the external URL of the server.
//
// The adapter holds no state. The service makes and stores each challenge,
// and gives it back with the response, so the adapter never trusts a
// challenge that the client sends: the check of the client data compares
// the challenge of the response with the challenge of the service.
package gowebauthn

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"opendustpan.org/opendustpan/pkg/mfa"
)

// ErrConfig reports an external URL that cannot give a relying party ID and
// an origin.
var ErrConfig = errors.New("the external URL cannot give a passkey relying party")

// DisplayName is the name of the relying party that an authenticator shows.
const DisplayName = "OpenDustpan"

// attestationNone is the one attestation format that the adapter accepts.
const attestationNone = "none"

// maxUserHandleBytes is the most bytes of a WebAuthn user handle. WebAuthn
// sets this limit. A person whose principal ID is longer cannot register a
// passkey: the principal IDs that the server makes have 24 characters.
const maxUserHandleBytes = 64

// RelyingParty is the adapter. Make it with New.
type RelyingParty struct {
	lib    *webauthn.WebAuthn
	rpID   string
	origin string
}

var _ mfa.RelyingParty = (*RelyingParty)(nil)

// New returns the relying party of the external URL externalURL. The
// relying party ID is the host name of the URL, and the origin is its scheme,
// host and port. The URL is https, or http on the host localhost only,
// because a browser runs WebAuthn only in a secure context. It returns
// ErrConfig for a URL that has no host, has user information, a query or a
// fragment, or names an IP address.
func New(externalURL string) (*RelyingParty, error) {
	site, err := Origin(externalURL)
	if err != nil {
		return nil, err
	}

	rpID, origin := site.RPID, site.Origin

	// The zero policies of attestation and signature are the defaults of
	// the module: it sets them to its explicit values in New.
	var (
		attestation protocol.AttestationPolicy
		signature   protocol.SignaturePolicy
	)

	// The service enforces the lifetime of a challenge in the store, so the
	// module enforces no timeout of its own.
	timeout := webauthn.TimeoutConfig{Enforce: false, Timeout: mfa.ChallengeLifetime, TimeoutUVD: mfa.ChallengeLifetime}

	lib, err := webauthn.New(&webauthn.Config{
		RPID:                        rpID,
		RPDisplayName:               DisplayName,
		RPOrigins:                   []string{origin},
		RPOpaqueOrigins:             nil,
		RPTopOrigins:                nil,
		RPTopOriginVerificationMode: protocol.TopOriginExplicitVerificationMode,
		RPAllowCrossOrigin:          false,
		AttestationPreference:       protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			AuthenticatorAttachment: "",
			RequireResidentKey:      protocol.ResidentKeyRequired(),
			ResidentKey:             protocol.ResidentKeyRequirementRequired,
			UserVerification:        protocol.VerificationRequired,
		},
		Debug:                             false,
		EncodeUserIDAsString:              false,
		Timeouts:                          webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
		MDS:                               nil,
		Attestation:                       attestation,
		Signature:                         signature,
		Filtering:                         nil,
		ExtensionsUnsolicitedOutputPolicy: protocol.UnsolicitedOutputPolicyReject,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	return &RelyingParty{lib: lib, rpID: rpID, origin: origin}, nil
}

// Site is the relying party ID and the origin of an external URL.
type Site struct {
	RPID   string
	Origin string
}

// Origin returns the relying party ID and the origin of the external URL
// raw, with the checks of New.
func Origin(raw string) (Site, error) {
	var none Site

	u, err := url.Parse(raw)
	if err != nil {
		return none, fmt.Errorf("%w: the URL does not parse", ErrConfig)
	}

	if partsErr := checkParts(u, raw); partsErr != nil {
		return none, partsErr
	}

	if hostErr := checkHost(u); hostErr != nil {
		return none, hostErr
	}

	return Site{RPID: u.Hostname(), Origin: u.Scheme + "://" + u.Host}, nil
}

// checkParts returns ErrConfig for a URL u, parsed from raw, with no scheme
// or host, with user information, a query or a fragment.
func checkParts(u *url.URL, raw string) error {
	switch {
	case !u.IsAbs() || u.Opaque != "" || u.Hostname() == "":
		return fmt.Errorf("%w: the URL has no scheme and host", ErrConfig)
	case u.User != nil:
		return fmt.Errorf("%w: the URL has user information", ErrConfig)
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#"):
		return fmt.Errorf("%w: the URL has a query or a fragment", ErrConfig)
	default:
		return nil
	}
}

// checkHost returns ErrConfig for a URL u that a browser cannot use as the
// origin of a passkey: not https, or http on a host other than localhost, a
// host with upper case letters, or a host that is not a domain name.
func checkHost(u *url.URL) error {
	host := u.Hostname()

	switch {
	case u.Scheme != "https" && (u.Scheme != "http" || host != "localhost"):
		return fmt.Errorf("%w: the URL is not https, or http on localhost", ErrConfig)
	case host != strings.ToLower(host):
		return fmt.Errorf("%w: the host has upper case letters", ErrConfig)
	case protocol.ValidateRPID(host) != nil:
		return fmt.Errorf("%w: the host is not a domain name", ErrConfig)
	default:
		return nil
	}
}

// RPID returns the relying party ID.
func (r *RelyingParty) RPID() string {
	return r.rpID
}

// AllowedOrigin returns the one origin that a response can name.
func (r *RelyingParty) AllowedOrigin() string {
	return r.origin
}

// user is the webauthn.User of a person and its passkeys.
type user struct {
	person mfa.Person
	keys   []webauthn.Credential
}

func (u user) WebAuthnID() []byte                         { return []byte(u.person.ID) }
func (u user) WebAuthnName() string                       { return u.person.Name }
func (u user) WebAuthnDisplayName() string                { return u.person.DisplayName }
func (u user) WebAuthnCredentials() []webauthn.Credential { return u.keys }

// credentialOf returns the library credential of the passkey p.
func credentialOf(p mfa.Passkey) webauthn.Credential {
	ts := make([]protocol.AuthenticatorTransport, 0, len(p.Transports))
	for _, t := range p.Transports {
		ts = append(ts, protocol.AuthenticatorTransport(t))
	}

	var c webauthn.Credential

	c.ID = bytes.Clone(p.CredentialID)
	c.PublicKey = bytes.Clone(p.PublicKey)
	c.Transport = ts
	c.Flags.BackupEligible = p.BackupEligible
	c.Authenticator.AAGUID = bytes.Clone(p.AAGUID[:])
	c.Authenticator.SignCount = p.SignCount

	return c
}

// descriptors returns the credential descriptors of ps.
func descriptors(ps []mfa.Passkey) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(ps))
	for i := range ps {
		c := credentialOf(ps[i])
		out = append(out, c.Descriptor())
	}

	return out
}

// checkPerson returns mfa.ErrInvalid for a person whose ID cannot be a user
// handle or who has no name.
func checkPerson(p mfa.Person) error {
	if p.ID == "" || len(p.ID) > maxUserHandleBytes || p.Name == "" {
		return fmt.Errorf("%w: the person has no valid ID or name", mfa.ErrInvalid)
	}

	return nil
}

// checkChallenge returns mfa.ErrInvalid unless c has mfa.ChallengeBytes.
func checkChallenge(c []byte) error {
	if len(c) != mfa.ChallengeBytes {
		return fmt.Errorf("%w: the challenge has %d bytes, want %d", mfa.ErrInvalid, len(c), mfa.ChallengeBytes)
	}

	return nil
}

// withChallenge sets the challenge of a register ceremony to c.
func withChallenge(c []byte) webauthn.RegistrationOption {
	return func(o *protocol.PublicKeyCredentialCreationOptions) error {
		o.Challenge = bytes.Clone(c)

		return nil
	}
}

// CreationOptions implements mfa.RelyingParty.
func (r *RelyingParty) CreationOptions(p mfa.Person, c []byte, exclude []mfa.Passkey) ([]byte, error) {
	if err := checkPerson(p); err != nil {
		return nil, err
	}

	if err := checkChallenge(c); err != nil {
		return nil, err
	}

	creation, _, err := r.lib.BeginRegistration(
		user{person: p, keys: nil},
		withChallenge(c),
		webauthn.WithExclusions(descriptors(exclude)),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: creation options: %w", mfa.ErrInvalid, err)
	}

	out, err := json.Marshal(creation)
	if err != nil {
		return nil, fmt.Errorf("%w: encode creation options: %w", mfa.ErrInvalid, err)
	}

	return out, nil
}

// libVerification returns the library value of v.
func libVerification(v mfa.UserVerification) protocol.UserVerificationRequirement {
	if v == mfa.VerificationPreferred {
		return protocol.VerificationPreferred
	}

	return protocol.VerificationRequired
}

// RequestOptions implements mfa.RelyingParty.
func (r *RelyingParty) RequestOptions(k mfa.Ceremony, c []byte, allow []mfa.Passkey) ([]byte, error) {
	if err := checkChallenge(c); err != nil {
		return nil, err
	}

	opts := []webauthn.LoginOption{
		webauthn.WithChallenge(bytes.Clone(c)),
		webauthn.WithUserVerification(libVerification(mfa.VerificationOf(k))),
	}

	var (
		assertion *protocol.CredentialAssertion
		err       error
	)

	switch {
	case k == mfa.CeremonySignIn && len(allow) == 0:
		assertion, _, err = r.lib.BeginDiscoverableLogin(opts...)
	case (k == mfa.CeremonySecondFactor || k == mfa.CeremonyFresh) && len(allow) > 0:
		opts = append(opts, webauthn.WithAllowedCredentials(descriptors(allow)))
		assertion, _, err = r.lib.BeginDiscoverableLogin(opts...)
	default:
		return nil, fmt.Errorf("%w: no assertion options for a %s ceremony with %d passkeys", mfa.ErrInvalid, k,
			len(allow))
	}

	if err != nil {
		return nil, fmt.Errorf("%w: request options: %w", mfa.ErrInvalid, err)
	}

	out, err := json.Marshal(assertion)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request options: %w", mfa.ErrInvalid, err)
	}

	return out, nil
}

// errNotThePasskey reports a discoverable assertion whose credential ID or
// user handle is not of the passkey of the service.
var errNotThePasskey = errors.New("the credential or the user handle is not of the passkey")

// The members of a response and of its client data that Head reads. The
// names are the names of the WebAuthn JSON.
const (
	memberRawID          = "rawId"
	memberResponse       = "response"
	memberClientDataJSON = "clientDataJSON"
	memberUserHandle     = "userHandle"
	memberChallenge      = "challenge"
)

// decode64 decodes the base64url text s, with or without padding.
func decode64(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("%w: a value is not base64url", mfa.ErrInvalid)
	}

	return b, nil
}

// members decodes the JSON object data into its members.
func members(data []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil, fmt.Errorf("%w: a value is not a JSON object", mfa.ErrInvalid)
	}

	return m, nil
}

// text returns the string member name of m, or "" when m has none.
func text(m map[string]json.RawMessage, name string) (string, error) {
	raw, ok := m[name]
	if !ok || string(raw) == "null" {
		return "", nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%w: the member %s is not a string", mfa.ErrInvalid, name)
	}

	return s, nil
}

// checkSize returns mfa.ErrInvalid for an empty response or one above
// mfa.MaxResponseBytes.
func checkSize(response []byte) error {
	if len(response) == 0 || len(response) > mfa.MaxResponseBytes {
		return fmt.Errorf("%w: the response has %d bytes, want 1 to %d", mfa.ErrInvalid, len(response),
			mfa.MaxResponseBytes)
	}

	return nil
}

// Head implements mfa.RelyingParty. It reads the JSON with the standard
// library only, so no parser of the module runs on a response before the
// service finds its challenge.
func (r *RelyingParty) Head(response []byte) (mfa.ResponseHead, error) {
	var none mfa.ResponseHead

	if err := checkSize(response); err != nil {
		return none, err
	}

	top, err := members(response)
	if err != nil {
		return none, err
	}

	id, err := decodeMember(top, memberRawID, 1, mfa.MaxCredentialIDBytes)
	if err != nil {
		return none, err
	}

	inner, err := members(top[memberResponse])
	if err != nil {
		return none, err
	}

	data, err := decodeMember(inner, memberClientDataJSON, 1, mfa.MaxResponseBytes)
	if err != nil {
		return none, err
	}

	handle, err := decodeMember(inner, memberUserHandle, 0, maxUserHandleBytes)
	if err != nil {
		return none, err
	}

	client, err := members(data)
	if err != nil {
		return none, err
	}

	challenge, err := decodeMember(client, memberChallenge, mfa.ChallengeBytes, mfa.ChallengeBytes)
	if err != nil {
		return none, err
	}

	return mfa.ResponseHead{Challenge: challenge, CredentialID: id, UserHandle: handle}, nil
}

// decodeMember returns the base64url member name of m, with min to most
// bytes, or mfa.ErrInvalid.
func decodeMember(m map[string]json.RawMessage, name string, least, most int) ([]byte, error) {
	s, err := text(m, name)
	if err != nil {
		return nil, err
	}

	b, err := decode64(s)
	if err != nil {
		return nil, err
	}

	if len(b) < least || len(b) > most {
		return nil, fmt.Errorf("%w: the member %s has %d bytes, want %d to %d", mfa.ErrInvalid, name, len(b), least,
			most)
	}

	return b, nil
}

// VerifyCreation implements mfa.RelyingParty.
func (r *RelyingParty) VerifyCreation(p mfa.Person, c, response []byte) (mfa.Registration, error) {
	var none mfa.Registration

	if err := checkPerson(p); err != nil {
		return none, err
	}

	if err := checkChallenge(c); err != nil {
		return none, err
	}

	if err := checkSize(response); err != nil {
		return none, err
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return none, fmt.Errorf("%w: the response does not parse", mfa.ErrInvalid)
	}

	// The relying party asks for attestation "none" and keeps no allowlist
	// of authenticator models. A response of another format would make the
	// module parse and check a statement that the server does not use, so
	// the adapter refuses it before any check of a statement runs.
	if format := parsed.Response.AttestationObject.Format; format != attestationNone {
		return none, fmt.Errorf("%w: attestation format %s, want %s", mfa.ErrVerification,
			strconv.Quote(strings.Map(printable, format)), attestationNone)
	}

	s := r.session(c, []byte(p.ID), nil, mfa.VerificationRequired)

	cred, err := r.lib.CreateCredential(user{person: p, keys: nil}, s, parsed)
	if err != nil {
		return none, fmt.Errorf("%w: %s", mfa.ErrVerification, reason(err))
	}

	return registrationOf(cred)
}

// registrationOf returns the registration of the library credential cred,
// with the limits of a passkey.
func registrationOf(cred *webauthn.Credential) (mfa.Registration, error) {
	var none mfa.Registration

	if cred == nil {
		return none, fmt.Errorf("%w: no credential", mfa.ErrVerification)
	}

	if len(cred.ID) == 0 || len(cred.ID) > mfa.MaxCredentialIDBytes ||
		len(cred.PublicKey) == 0 || len(cred.PublicKey) > mfa.MaxPublicKeyBytes {
		return none, fmt.Errorf("%w: the credential ID or the key is out of its limits", mfa.ErrInvalid)
	}

	var aaguid [16]byte

	switch len(cred.Authenticator.AAGUID) {
	case 0:
	case len(aaguid):
		copy(aaguid[:], cred.Authenticator.AAGUID)
	default:
		return none, fmt.Errorf("%w: the AAGUID has %d bytes", mfa.ErrInvalid, len(cred.Authenticator.AAGUID))
	}

	ts := make([]string, 0, len(cred.Transport))

	for _, t := range cred.Transport {
		// A browser can name a transport that this server does not know.
		// The allowlist keeps the known ones, and the passkey works without
		// the others.
		if mfa.ValidTransport(string(t)) && !slices.Contains(ts, string(t)) {
			ts = append(ts, string(t))
		}
	}

	return mfa.Registration{
		CredentialID:   bytes.Clone(cred.ID),
		PublicKey:      bytes.Clone(cred.PublicKey),
		Transports:     ts,
		AAGUID:         aaguid,
		SignCount:      cred.Authenticator.SignCount,
		BackupEligible: cred.Flags.BackupEligible,
		UserVerified:   cred.Flags.UserVerified,
	}, nil
}

// VerifyAssertion implements mfa.RelyingParty.
func (r *RelyingParty) VerifyAssertion(
	k mfa.Ceremony, owner mfa.Person, key mfa.Passkey, c, response []byte,
) (mfa.Assertion, error) {
	var none mfa.Assertion

	if k == mfa.CeremonyRegister || !mfa.ValidCeremony(k) {
		return none, fmt.Errorf("%w: a %s ceremony has no assertion", mfa.ErrInvalid, k)
	}

	if err := checkPerson(owner); err != nil {
		return none, err
	}

	if err := checkChallenge(c); err != nil {
		return none, err
	}

	if err := checkSize(response); err != nil {
		return none, err
	}

	// Defense in depth: the service reads the passkey of the owner from the
	// store. This check stays so that the adapter never checks a signature
	// against the key of another person.
	if key.PersonID != owner.ID {
		return none, fmt.Errorf("%w: the passkey is not of the person", mfa.ErrVerification)
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return none, fmt.Errorf("%w: the response does not parse", mfa.ErrInvalid)
	}

	u := user{person: owner, keys: []webauthn.Credential{credentialOf(key)}}
	v := mfa.VerificationOf(k)

	if k == mfa.CeremonySignIn {
		s := r.session(c, nil, nil, v)
		_, _, err = r.lib.ValidatePasskeyLogin(discoverable(owner, key, u), s, parsed)
	} else {
		s := r.session(c, []byte(owner.ID), [][]byte{key.CredentialID}, v)
		_, err = r.lib.ValidateLogin(u, s, parsed)
	}

	if err != nil {
		return none, fmt.Errorf("%w: %s", mfa.ErrVerification, reason(err))
	}

	flags := parsed.Response.AuthenticatorData.Flags

	return mfa.Assertion{
		SignCount:    parsed.Response.AuthenticatorData.Counter,
		UserVerified: flags.HasUserVerified(),
	}, nil
}

// session returns the session data of a ceremony with the challenge c.
func (r *RelyingParty) session(c, userID []byte, allow [][]byte, v mfa.UserVerification) webauthn.SessionData {
	// The service asks for no extension, so the session holds none.
	var none protocol.SessionExtensions

	return webauthn.SessionData{
		Challenge:                 base64.RawURLEncoding.EncodeToString(c),
		RelyingPartyID:            r.rpID,
		Origin:                    "",
		UserID:                    userID,
		AllowedCredentialIDs:      allow,
		Expires:                   time.Time{},
		UserVerification:          libVerification(v),
		Extensions:                none,
		CredParams:                webauthn.CredentialParametersDefault(),
		Mediation:                 protocol.MediationDefault,
		AuthorizeUVInitialization: false,
	}
}

// discoverable returns the handler of a discoverable assertion: it gives
// the module the user u of the passkey key of owner, and refuses a response
// whose credential ID is not the ID of key or whose user handle is not the ID
// of owner.
//
// Defense in depth: the service finds key by the credential ID of the
// response and compares the user handle, and the module compares the user
// handle and the credential with the user again. This check stays so that the
// adapter never gives the module a user that the response does not name.
func discoverable(owner mfa.Person, key mfa.Passkey, u webauthn.User) webauthn.DiscoverableUserHandler {
	return func(rawID, userHandle []byte) (webauthn.User, error) {
		if !bytes.Equal(rawID, key.CredentialID) || !bytes.Equal(userHandle, []byte(owner.ID)) {
			return nil, errNotThePasskey
		}

		return u, nil
	}
}

// reason returns a short text of a check that failed. It keeps the type and
// the details of a protocol error and drops the rest, so that no value of the
// response reaches a log.
func reason(err error) string {
	if perr, ok := errors.AsType[*protocol.Error](err); ok {
		return strings.Map(printable, perr.Type+": "+perr.Details)
	}

	return "the check failed"
}

// printable maps each control character to a space.
func printable(r rune) rune {
	if r < 0x20 || r == 0x7f {
		return ' '
	}

	return r
}
