package audit

// Bounds of the client-influenced text fields of an entry, in bytes. The
// audit adapters cap each field at its bound before they append, so a client
// cannot grow an entry past what a log sink holds: the entry is hashed and
// stored after the cap, so the chain covers the capped values. Each bound
// sits above the longest value the server itself writes, so capping never
// changes a legitimate entry: object references are validated names of at
// most a few hundred bytes, value paths of real state are short, subjects
// made through the API hold at most 255 bytes, a client address is an IP
// string of at most 45 bytes, and a stable unit ID is a prefix and 20
// characters.
const (
	// MaxObjectLen caps the object reference of an entry, in bytes.
	MaxObjectLen = 512
	// MaxValuePathLen caps the value path of an entry, in bytes. Reveal
	// request bodies hold at most 8 KiB, so a longer path never resolves.
	MaxValuePathLen = 1024
	// MaxSubjectLen caps the subject of an entry, in bytes.
	MaxSubjectLen = 512
	// MaxIDPSubjectLen caps the identity provider subject of an entry, in
	// bytes. The subject claim of a provider has no length check, so the
	// cap sits here.
	MaxIDPSubjectLen = 512
	// MaxSourceIPLen caps the client address of an entry, in bytes.
	MaxSourceIPLen = 64
	// MaxObjectIDLen caps the stable unit ID of an entry, in bytes.
	MaxObjectIDLen = 64
	// MaxChangeTextLen caps each text of one change (key, old and new
	// value, layer, takes effect), in bytes. The count of changes already
	// stops at MaxChanges.
	MaxChangeTextLen = 1024
)

// TruncateUserAgent caps s at MaxUserAgentLen bytes on a UTF-8 rune
// boundary, so a multi-byte rune is never split. Every HTTP adapter uses
// this helper, so each one caps the same way.
func TruncateUserAgent(s string) string {
	return capText(s, MaxUserAgentLen)
}

// Bound returns a copy of e with every client-influenced text field capped
// at its bound on a UTF-8 rune boundary: user agent, object, value path,
// subject, identity provider subject, client address, stable unit ID and
// each text of each change. An entry that already fits reads back unchanged.
// Call it where the entry is built, before the store hashes and keeps it,
// so the chain covers the capped values.
func Bound(e Entry) Entry {
	out := e
	out.UserAgent = capText(e.UserAgent, MaxUserAgentLen)
	out.Object = capText(e.Object, MaxObjectLen)
	out.ValuePath = capText(e.ValuePath, MaxValuePathLen)
	out.Subject = capText(e.Subject, MaxSubjectLen)
	out.IDPSubject = capText(e.IDPSubject, MaxIDPSubjectLen)
	out.SourceIP = capText(e.SourceIP, MaxSourceIPLen)
	out.ObjectID = capText(e.ObjectID, MaxObjectIDLen)
	out.Changes = boundChanges(e.Changes)

	return out
}

// boundChanges returns changes with each text capped at MaxChangeTextLen.
// A nil list stays nil, so an entry without changes keeps none.
func boundChanges(changes []Change) []Change {
	if changes == nil {
		return nil
	}

	out := make([]Change, len(changes))

	for i, c := range changes {
		out[i] = Change{
			Key:         capText(c.Key, MaxChangeTextLen),
			Old:         capOld(c.Old),
			New:         capText(c.New, MaxChangeTextLen),
			Layer:       capText(c.Layer, MaxChangeTextLen),
			TakesEffect: capText(c.TakesEffect, MaxChangeTextLen),
		}
	}

	return out
}

// capOld caps the old value of a change, keeping nil as nil: a nil old
// value records a value and no change.
func capOld(old *string) *string {
	if old == nil {
		return nil
	}

	capped := capText(*old, MaxChangeTextLen)

	return &capped
}

// capText caps s at max bytes without splitting a UTF-8 sequence. A value
// that already fits reads back unchanged.
func capText(s string, max int) string {
	if len(s) <= max {
		return s
	}

	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}

	return s[:cut]
}
