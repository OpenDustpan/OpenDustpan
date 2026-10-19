package unitimport

import (
	"fmt"
	"strings"
	"unicode"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxUnits is the largest count of units that one import moves. A larger
// source needs more than one import.
const MaxUnits = 1000

// MaxTextBytes is the limit of an ID, a name, a reason, an error code and
// an address in bytes.
const MaxTextBytes = 1024

// text checks one field of a record: not empty (when needed), within
// MaxTextBytes, and with no control character.
func text(field, v string, needed bool) error {
	switch {
	case v == "" && needed:
		return fmt.Errorf("%w: %s is empty", ErrInvalid, field)
	case len(v) > MaxTextBytes:
		return fmt.Errorf("%w: %s is longer than %d bytes", ErrInvalid, field, MaxTextBytes)
	case strings.IndexFunc(v, unicode.IsControl) >= 0:
		return fmt.Errorf("%w: %s holds a control character", ErrInvalid, field)
	}

	return nil
}

// Valid reports whether k is a source kind.
func (k SourceKind) Valid() bool {
	switch k {
	case SourceTFEAPI, SourcePulumiService, SourceS3, SourceOpenTofuHTTP, SourceUpload:
		return true
	}

	return false
}

// Valid reports whether w is a credential way.
func (w CredentialWay) Valid() bool {
	switch w {
	case CredentialProfile, CredentialEphemeral, CredentialNone:
		return true
	}

	return false
}

// Validate checks the tool and the name of a source unit with the text rules
// of the import records. It wraps ErrInvalid.
func (u SourceUnit) Validate() error {
	if err := text("source tool", u.Tool, true); err != nil {
		return err
	}

	return text("source name", u.Name, true)
}

// ValidateNew checks an import before its first store. It wraps ErrInvalid
// or ErrAddressInvalid. The upload source has no address and needs no
// credential. Each other source needs an address that CheckAddress accepts
// and a credential way.
func (i Import) ValidateNew() error {
	if err := text("import ID", i.ID, true); err != nil {
		return err
	}

	if _, err := namespace.ParseName(i.Namespace.String()); err != nil {
		return fmt.Errorf("%w: namespace: %w", ErrInvalid, err)
	}

	if !i.Kind.Valid() {
		return fmt.Errorf("%w: source kind %q is not known", ErrInvalid, i.Kind)
	}

	if !i.Credential.Valid() {
		return fmt.Errorf("%w: credential way %q is not known", ErrInvalid, i.Credential)
	}

	if err := text("created by", i.CreatedBy, true); err != nil {
		return err
	}

	if i.CreatedAt.IsZero() {
		return fmt.Errorf("%w: creation time is empty", ErrInvalid)
	}

	for _, f := range []struct{ name, v string }{
		{"profile", i.Profile}, {"profile path", i.ProfilePath}, {"reason", i.Reason},
	} {
		if err := text(f.name, f.v, false); err != nil {
			return err
		}
	}

	if i.Options.HistoryLimit < 0 {
		return fmt.Errorf("%w: history limit is negative", ErrInvalid)
	}

	for _, name := range i.Selection.Names {
		if err := text("selected name", name, true); err != nil {
			return err
		}
	}

	return i.validateSource()
}

func (i Import) validateSource() error {
	if i.Kind == SourceUpload {
		if i.Address != "" || i.Credential != CredentialNone || i.ProfilePath != "" {
			return fmt.Errorf("%w: an upload import has no address and no credential", ErrInvalid)
		}

		return nil
	}

	if i.Credential == CredentialNone {
		return fmt.Errorf("%w: a %s import needs a credential", ErrInvalid, i.Kind)
	}

	if i.Credential != CredentialProfile && i.ProfilePath != "" {
		return fmt.Errorf("%w: only a profile import names a profile path", ErrInvalid)
	}

	if (i.Credential == CredentialProfile) != (i.Profile != "") {
		return fmt.Errorf("%w: a profile import names a profile, and no other import does", ErrInvalid)
	}

	return CheckAddress(i.Address)
}

// ValidateNew checks an import unit before it is stored in the import id.
// It wraps ErrInvalid. A new unit is planned or skipped, with no committed
// version and no target yet.
func (u ImportUnit) ValidateNew(id string) error {
	for _, f := range []struct {
		name, v string
		needed  bool
	}{
		{"import unit ID", u.ID, true},
		{"target name", u.TargetName, true},
		{"error code", u.ErrorCode, false},
		{"encryption", u.Encryption, false},
	} {
		if err := text(f.name, f.v, f.needed); err != nil {
			return err
		}
	}

	if err := u.Source.Validate(); err != nil {
		return err
	}

	switch {
	case u.ImportID != "" && u.ImportID != id:
		return fmt.Errorf("%w: unit %q names import %q, not %q", ErrInvalid, u.ID, u.ImportID, id)
	case u.State != UnitPlanned && u.State != UnitSkipped:
		return fmt.Errorf("%w: unit %q starts in state %q, want planned or skipped", ErrInvalid, u.ID, u.State)
	case u.Versions < 0 || u.Bytes < 0 || u.Committed != 0:
		return fmt.Errorf("%w: unit %q has bad counts", ErrInvalid, u.ID)
	}

	return nil
}

// ValidateUnits checks the units of PutUnits: at most MaxUnits, each valid,
// with no repeated ID and no repeated target name.
func ValidateUnits(id string, units []ImportUnit) error {
	if len(units) > MaxUnits {
		return fmt.Errorf("%w: %d units, at most %d", ErrInvalid, len(units), MaxUnits)
	}

	ids := make(map[string]bool, len(units))
	targets := make(map[string]bool, len(units))

	for i := range units {
		if err := units[i].ValidateNew(id); err != nil {
			return err
		}

		key := strings.ToLower(units[i].TargetName)
		if ids[units[i].ID] || targets[key] {
			return fmt.Errorf("%w: unit %q repeats an ID or a target name", ErrInvalid, units[i].ID)
		}

		ids[units[i].ID], targets[key] = true, true
	}

	return nil
}

// ValidateCounts checks the counts of SetCounts.
func ValidateCounts(c Counts) error {
	if c.Versions < 0 || c.Committed < 0 || c.Committed > c.Versions {
		return fmt.Errorf("%w: %d committed of %d versions", ErrInvalid, c.Committed, c.Versions)
	}

	return nil
}

// Validate checks a ledger row before a Tool stores it.
func (r LedgerRow) Validate() error {
	for _, f := range []struct{ name, v string }{
		{"import ID", r.ImportID},
		{"import unit ID", r.ImportUnitID},
		{"source version", string(r.Version)},
		{"source digest", r.SourceSHA256},
		{"stored digest", r.StoredSHA256},
	} {
		if err := text(f.name, f.v, true); err != nil {
			return err
		}
	}

	if r.CommittedAt.IsZero() {
		return fmt.Errorf("%w: commit time is empty", ErrInvalid)
	}

	if err := r.Item.Validate(); err != nil {
		return fmt.Errorf("%w: history item: %w", ErrInvalid, err)
	}

	return nil
}
