// Package firstadminstore holds the input checks that both store adapters of
// the first administrator run before they open a transaction.
package firstadminstore

import (
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/principal"
)

// maxIDLength is the longest ID that a store accepts.
const maxIDLength = 64

// CheckOffer returns an error that wraps a sentinel of firstadmin when in is
// not valid. Each store runs it first.
//
// Defense in depth: the service checks the same fields, and the schema checks
// the digest and the lifetime. This check stays so that a new caller of the
// port cannot store a row that the service would refuse.
func CheckOffer(in firstadmin.OfferInput) error {
	if in.Now.IsZero() {
		return fmt.Errorf("%w: no time", firstadmin.ErrInvalid)
	}

	if !firstadmin.ValidDigest(in.Digest) {
		return fmt.Errorf(
			"%w: the digest is not %d lowercase hex characters",
			firstadmin.ErrInvalid,
			firstadmin.DigestLength,
		)
	}

	if !principalID(in.PrincipalID) {
		return firstadmin.ErrPrincipalNotPerson
	}

	if in.CreatedBy == "" || ident.HasHidden(in.CreatedBy) {
		return fmt.Errorf("%w: the creator is blank or holds a hidden character", firstadmin.ErrInvalid)
	}

	return firstadmin.ValidateReason(in.Reason)
}

// CheckRedeem returns an error that wraps a sentinel of firstadmin when in is
// not valid. A digest of the wrong form gives ErrCodeInvalid, the one answer
// of every code failure. Each store runs it first.
//
// Defense in depth: the service checks the form of the code, and the
// transaction compares the digest in constant time. This check stays so that
// a new caller of the port cannot reach the compare with a digest that no code
// makes.
func CheckRedeem(in firstadmin.RedeemInput) error {
	if !firstadmin.ValidDigest(in.Digest) {
		return firstadmin.ErrCodeInvalid
	}

	if !principalID(in.PrincipalID) {
		return firstadmin.ErrPrincipalNotPerson
	}

	if in.Now.IsZero() {
		return fmt.Errorf("%w: no time", firstadmin.ErrInvalid)
	}

	if in.SessionID == "" || ident.HasHidden(in.SessionID) {
		return fmt.Errorf("%w: the session ID is blank or holds a hidden character", firstadmin.ErrInvalid)
	}

	if !strings.HasPrefix(in.GrantID, access.GrantIDPrefix) || !ident.Valid(in.GrantID, maxIDLength) {
		return fmt.Errorf("%w: the grant ID is not a grant ID", firstadmin.ErrInvalid)
	}

	return nil
}

// principalID reports whether id has the form of a stable principal ID.
func principalID(id string) bool {
	return len(id) > len(principal.IDPrefix) && strings.HasPrefix(id, principal.IDPrefix) &&
		ident.Valid(id, maxIDLength)
}
