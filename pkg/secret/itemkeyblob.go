package secret

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxWrappedItemKeyBytes bounds the size of one wrapped item key blob. A
// 256-bit item key with the nonce and the tag of the wrapper is far smaller.
const MaxWrappedItemKeyBytes = 4096

// MaxItemKeyVersion bounds the version of an item key blob. The version is the
// version of the namespace key that wraps the item key, and each rewrap writes a
// new blob, so a real value stays small.
const MaxItemKeyVersion = 1 << 20

// ItemKeyBlobGracePeriod is how long after its write a wrapped item key blob
// that no committed record names stays before it is garbage. A writer puts the
// blob before it commits the record, so a blob without a record can belong to
// a write that still commits. The store ends each item key transaction at
// ItemKeyAttemptDeadline (30 minutes), so no write commits later than that
// after its put. The period of 1 hour is longer than the deadline, plus the
// lease of an instance (30 seconds), plus ItemKeyBlobClockSkew (15 minutes),
// the largest clock difference between the server and the blob store that the
// rule allows.
const ItemKeyBlobGracePeriod = time.Hour

// attemptLen is the length of the attempt part of a blob key: the length of
// an ID of ident.NewID with no prefix.
const attemptLen = 20

// ItemKeyFamily is the family of the item key blobs.
func ItemKeyFamily() scopeaudit.Family {
	return scopeaudit.Family{Blobs: "item key blob", Dir: strings.TrimSuffix(itemKeyDir, "/")}
}

// ItemKeyBlobs reads and writes the wrapped item key blobs. It builds a
// *blob.Scoped for each call, from the namespace ID that the caller took from
// the row that it resolved and authorized, never from a request field. Each
// blob key is namespaces/<namespace ID>/item-keys/<item key ID>/v<version>-<attempt>.
//
// A call with a key of another namespace returns blob.ErrOutOfScope before any
// call to the store, and writes one refusal to the scopeaudit.Recorder. A read
// of a blob that does not match its record returns ErrItemKeyBlobMismatch and
// writes one refusal to the scopeaudit.IntegrityRecorder.
type ItemKeyBlobs struct {
	store      blob.Store
	refusals   scopeaudit.Recorder
	mismatches scopeaudit.IntegrityRecorder
}

// NewItemKeyBlobs returns ItemKeyBlobs over store. All arguments are
// required.
func NewItemKeyBlobs(
	store blob.Store, refusals scopeaudit.Recorder, mismatches scopeaudit.IntegrityRecorder,
) (*ItemKeyBlobs, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: blob store", ErrMissingDependency)
	}

	if refusals == nil {
		return nil, fmt.Errorf("%w: out-of-scope recorder", ErrMissingDependency)
	}

	if mismatches == nil {
		return nil, fmt.Errorf("%w: mismatch recorder", ErrMissingDependency)
	}

	return &ItemKeyBlobs{store: store, refusals: refusals, mismatches: mismatches}, nil
}

// itemKeyRel returns the key of version of id with the attempt part attempt,
// relative to the scope of a namespace. It returns ErrInvalidItemRef for an
// invalid ID, a version that is not in 1 to MaxItemKeyVersion, or an attempt
// part that is not valid.
func itemKeyRel(id ItemKeyID, version int, attempt string) (string, error) {
	if _, err := ParseItemKeyID(string(id)); err != nil {
		return "", err
	}

	if version < 1 || version > MaxItemKeyVersion {
		return "", fmt.Errorf("%w: item key version %d", ErrInvalidItemRef, version)
	}

	if !validAttempt(attempt) {
		return "", fmt.Errorf("%w: attempt part of an item key blob key", ErrInvalidItemRef)
	}

	return itemKeyDir + string(id) + "/v" + strconv.Itoa(version) + "-" + attempt, nil
}

// ValidItemKeyAttempt reports whether s is the attempt part of an item key
// blob key, as NewKey makes it. A store adapter checks the attempt part of a
// record with it.
func ValidItemKeyAttempt(s string) bool {
	return validAttempt(s)
}

// validAttempt reports whether s is an attempt part: attemptLen lowercase
// ASCII letters and digits, the form of ident.NewID with no prefix.
func validAttempt(s string) bool {
	if len(s) != attemptLen {
		return false
	}

	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}

	return true
}

// ItemKeyBlob names the parts of one item key blob key.
type ItemKeyBlob struct {
	Namespace namespace.ID
	ID        ItemKeyID
	Attempt   string
	Version   int
}

// ParseItemKeyBlobKey splits a blob key of the form
// namespaces/<namespace ID>/item-keys/<item key ID>/v<version>-<attempt> into
// its parts. A backup check, a sweep and the purge ledger use it to name
// the item key that a blob holds. It returns ErrInvalidItemRef for any other
// key.
func ParseItemKeyBlobKey(key blob.Key) (ItemKeyBlob, error) {
	parts := strings.Split(key.String(), "/")
	if len(parts) != 5 || parts[0] != "namespaces" || parts[2] != strings.TrimSuffix(itemKeyDir, "/") {
		return ItemKeyBlob{}, fmt.Errorf("%w: not an item key blob key", ErrInvalidItemRef)
	}

	ns, err := namespace.ParseID(parts[1])
	if err != nil {
		return ItemKeyBlob{}, fmt.Errorf("%w: namespace ID of the key", ErrInvalidItemRef)
	}

	id, err := ParseItemKeyID(parts[3])
	if err != nil {
		return ItemKeyBlob{}, err
	}

	version, attempt, ok := parseItemKeyVersion(parts[4])
	if !ok {
		return ItemKeyBlob{}, fmt.Errorf("%w: item key version", ErrInvalidItemRef)
	}

	return ItemKeyBlob{Namespace: ns, ID: id, Version: version, Attempt: attempt}, nil
}

// parseItemKeyVersion parses "v<version>-<attempt>": a version in 1 to
// MaxItemKeyVersion in its plain decimal form, with no sign and no leading
// zero, and a valid attempt part.
func parseItemKeyVersion(s string) (int, string, bool) {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return 0, "", false
	}

	digits, attempt, hasAttempt := strings.Cut(rest, "-")
	if !hasAttempt || !validAttempt(attempt) {
		return 0, "", false
	}

	n, err := strconv.Atoi(digits)
	if err != nil || digits != strconv.Itoa(n) {
		return 0, "", false
	}

	return n, attempt, n >= 1 && n <= MaxItemKeyVersion
}

// Key returns the blob key of version of the item key id with the attempt
// part attempt, in the namespace ns:
// namespaces/<ns>/item-keys/<id>/v<version>-<attempt>. A reader takes version
// and attempt from the record. A writer takes a new key from NewKey. A backup
// copies the blob like every other blob, and every backup in one target shares
// the copy, so one delete in the target reaches all of them. It returns
// ErrInvalidItemRef for an invalid ID, version or attempt part, and
// blob.ErrInvalidKey for an invalid namespace ID.
func (b *ItemKeyBlobs) Key(ns namespace.ID, id ItemKeyID, version int, attempt string) (blob.Key, error) {
	rel, err := itemKeyRel(id, version, attempt)
	if err != nil {
		return "", err
	}

	v, err := b.view(ns)
	if err != nil {
		return "", err
	}

	key, err := v.Key(rel)
	if err != nil {
		return "", fmt.Errorf("item key blob key: %w", err)
	}

	return key, nil
}

// NewKey returns a new blob key for one write of version of the item key id,
// in the namespace ns, and its attempt part. Each call makes a new random
// attempt part, so two writes never share a key: the first write of an item
// key, each retry, and each rewrap to a new version. The record of the write
// names the attempt part. It returns the errors of Key.
func (b *ItemKeyBlobs) NewKey(ns namespace.ID, id ItemKeyID, version int) (blob.Key, string, error) {
	attempt := ident.NewID("")

	key, err := b.Key(ns, id, version, attempt)
	if err != nil {
		return "", "", err
	}

	return key, attempt, nil
}

// Put stores the wrapped item key under key, in the namespace ns. key must
// have an attempt part, as NewKey gives it. It returns ErrTooLarge for more
// than MaxWrappedItemKeyBytes, blob.ErrOutOfScope when key is outside the
// namespace ns or outside its item-keys directory, ErrInvalidItemRef for a
// key that is not an item key blob key, for example a key with no attempt
// part, and blob.ErrExists when the store refuses a key that already holds a
// blob.
func (b *ItemKeyBlobs) Put(ctx context.Context, ns namespace.ID, key blob.Key, wrapped []byte) error {
	if len(wrapped) > MaxWrappedItemKeyBytes {
		return fmt.Errorf(
			"%w: wrapped item key has %d bytes, limit %d",
			ErrTooLarge,
			len(wrapped),
			MaxWrappedItemKeyBytes,
		)
	}

	v, err := b.scopedKey(ctx, ns, key, "put")
	if err != nil {
		return err
	}

	if err := v.Put(ctx, key, bytes.NewReader(wrapped)); err != nil {
		return b.refuse(ctx, ns, "put", fmt.Errorf("put item key blob: %w", err), scopeaudit.OutsideNamespace)
	}

	return nil
}

// Get returns the wrapped item key under key, in the namespace ns, after it
// compares its size and its MD5 with want, the blob info of the record that
// names key. The caller unwraps the bytes only after Get returns them. It
// returns blob.ErrOutOfScope when key is outside the namespace ns or outside
// its item-keys directory, ErrKeyNotFound when the blob is missing,
// ErrTooLarge for a blob over MaxWrappedItemKeyBytes, ErrInvalidItemRef when
// want does not name key, and ErrItemKeyBlobMismatch when the bytes do not
// match want. A mismatch writes one refusal to the IntegrityRecorder.
func (b *ItemKeyBlobs) Get(ctx context.Context, ns namespace.ID, key blob.Key, want ItemKeyBlobInfo) ([]byte, error) {
	v, err := b.scopedKey(ctx, ns, key, "get")
	if err != nil {
		return nil, err
	}

	// The record must name the key that the call reads, so that the size and
	// the MD5 of one blob never check the bytes of another blob. A size out of
	// range is a record that no writer made, so the read fails closed.
	parsed, err := ParseItemKeyBlobKey(key)
	if err != nil || parsed.Version != want.Version || parsed.Attempt != want.Attempt ||
		want.Size < 1 || want.Size > MaxWrappedItemKeyBytes {
		return nil, fmt.Errorf("%w: the blob info does not name the item key blob key", ErrInvalidItemRef)
	}

	rc, err := v.Get(ctx, key)
	if err != nil {
		return nil, b.refuse(ctx, ns, "get", mapItemKeyBlobError(err), scopeaudit.OutsideNamespace)
	}

	wrapped, err := readWrappedKey(rc)
	if err != nil {
		return nil, err
	}

	if err := b.checkDigest(ctx, ns, wrapped, want); err != nil {
		return nil, err
	}

	return wrapped, nil
}

// readWrappedKey reads at most MaxWrappedItemKeyBytes from rc and closes it.
func readWrappedKey(rc io.ReadCloser) ([]byte, error) {
	data, readErr := io.ReadAll(io.LimitReader(rc, MaxWrappedItemKeyBytes+1))

	if closeErr := rc.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}

	if readErr != nil {
		return nil, fmt.Errorf("read item key blob: %w", readErr)
	}

	if len(data) > MaxWrappedItemKeyBytes {
		return nil, fmt.Errorf("%w: item key blob is longer than %d bytes", ErrTooLarge, MaxWrappedItemKeyBytes)
	}

	return data, nil
}

// Delete removes the blob under key, in the namespace ns. A missing blob is
// not an error. It returns blob.ErrOutOfScope when key is outside the
// namespace ns or outside its item-keys directory.
func (b *ItemKeyBlobs) Delete(ctx context.Context, ns namespace.ID, key blob.Key) error {
	v, err := b.scopedKey(ctx, ns, key, "delete")
	if err != nil {
		return err
	}

	if err := v.Delete(ctx, key); err != nil {
		return b.refuse(ctx, ns, "delete", fmt.Errorf("delete item key blob: %w", err), scopeaudit.OutsideNamespace)
	}

	return nil
}

// Versions returns the blob keys of every version of the item key id in the
// namespace ns, in the order of the version number and then of the attempt
// part, for a purge or a sweep. It returns every attempt of each version,
// also the attempts that no record names. It returns ErrInvalidItemRef when
// the listing holds a key that is not a version of the item key id.
func (b *ItemKeyBlobs) Versions(ctx context.Context, ns namespace.ID, id ItemKeyID) ([]blob.Key, error) {
	if _, err := ParseItemKeyID(string(id)); err != nil {
		return nil, err
	}

	v, err := b.view(ns)
	if err != nil {
		return nil, err
	}

	var objects []blob.Object

	err = blob.Walk(ctx, v, v.Prefix()+itemKeyDir+string(id)+"/", func(o blob.Object) error {
		objects = append(objects, o)

		return nil
	})
	if err != nil {
		return nil, b.refuse(ctx, ns, "list", fmt.Errorf("list item key blobs: %w", err), scopeaudit.OutsideNamespace)
	}

	// Defense in depth: the view lists only under the prefix of the item key.
	// Each key is checked again, and it must name this namespace and this item
	// key, so a listing fault of an adapter never gives the purge a key of
	// another item key.
	parsed := make([]ItemKeyBlob, 0, len(objects))

	for i := range objects {
		part, parseErr := ParseItemKeyBlobKey(objects[i].Key)
		if parseErr != nil || part.Namespace != ns || part.ID != id {
			return nil, fmt.Errorf(
				"%w: the listing holds a key that is not a version of the item key",
				ErrInvalidItemRef,
			)
		}

		parsed = append(parsed, part)
	}

	// The order is the order of the version number: v2 comes before v10, which
	// the order of the key text does not give.
	slices.SortFunc(parsed, func(a, b ItemKeyBlob) int {
		return cmp.Or(cmp.Compare(a.Version, b.Version), cmp.Compare(a.Attempt, b.Attempt))
	})

	keys := make([]blob.Key, 0, len(parsed))

	for _, part := range parsed {
		key, keyErr := b.Key(ns, part.ID, part.Version, part.Attempt)
		if keyErr != nil {
			return nil, keyErr
		}

		keys = append(keys, key)
	}

	return keys, nil
}

// ItemKeyBlobNamer reports whether a committed record names the item key blob
// b. A reaper gives it, from the item key records of its store. It returns an
// error when it cannot read the records.
type ItemKeyBlobNamer func(ctx context.Context, b ItemKeyBlob) (bool, error)

// clockProbePrefix starts the name of the probe blob of a reaper run, in the
// item-keys directory of a namespace. No item key ID has this form.
const clockProbePrefix = "clock-probe-"

// Garbage returns the item key blobs of the namespace ns that a reaper can
// delete at the time now, the time of the server: the blobs that no committed
// record names, and that are older than ItemKeyBlobGracePeriod. A writer puts
// a blob before it commits the record that names it, so a younger blob can
// belong to a write that still commits, and Garbage never returns it. Garbage
// deletes no item key blob. The caller deletes the keys after it checks that
// no backup runs.
//
// The age of a blob comes from the clock of the blob store. So each run first
// puts a probe blob with a new name, reads its time from the listing, and
// deletes it at the end. When that time differs from now by more than
// ItemKeyBlobClockSkew, ahead or behind, the run returns
// ErrItemKeyBlobClockSkew and no key. A probe blob of an earlier run that a
// failed delete left is garbage after the grace period.
//
// Garbage fails closed: a blob with a time of zero or a time after now, and a
// key that is not an item key blob key of ns, are never garbage, and an error
// of named, of the probe or of the listing returns the error and no key. A
// blob with a time more than ItemKeyBlobClockSkew after now returns
// ErrItemKeyBlobClockSkew and no key.
func (b *ItemKeyBlobs) Garbage(
	ctx context.Context, ns namespace.ID, named ItemKeyBlobNamer, now time.Time,
) ([]blob.Key, error) {
	if named == nil {
		return nil, fmt.Errorf("%w: item key blob namer", ErrMissingDependency)
	}

	if now.IsZero() {
		return nil, fmt.Errorf("%w: a reaper needs the time", ErrInvalidItemRef)
	}

	v, err := b.view(ns)
	if err != nil {
		return nil, err
	}

	probe, err := v.Key(itemKeyDir + clockProbePrefix + ident.NewID(""))
	if err != nil {
		return nil, fmt.Errorf("clock probe key: %w", err)
	}

	if err = v.Put(ctx, probe, bytes.NewReader(nil)); err != nil {
		return nil, b.refuse(ctx, ns, "put", fmt.Errorf("put the clock probe: %w", err), scopeaudit.OutsideNamespace)
	}

	var objects []blob.Object

	listErr := blob.Walk(ctx, v, v.Prefix()+itemKeyDir, func(o blob.Object) error {
		objects = append(objects, o)

		return nil
	})

	// The probe goes in every case. A failed delete fails the run, and a later
	// run finds the probe as garbage.
	if delErr := v.Delete(context.WithoutCancel(ctx), probe); delErr != nil {
		return nil, fmt.Errorf("delete the clock probe: %w", delErr)
	}

	if listErr != nil {
		return nil, b.refuse(
			ctx,
			ns,
			"list",
			fmt.Errorf("list item key blobs: %w", listErr),
			scopeaudit.OutsideNamespace,
		)
	}

	if clockErr := checkProbeClock(objects, probe, now); clockErr != nil {
		return nil, clockErr
	}

	run := garbageRun{ns: ns, named: named, now: now, probe: probe}

	return run.filter(ctx, objects)
}

// checkProbeClock finds the probe in objects and returns
// ErrItemKeyBlobClockSkew when its time differs from now by more than
// ItemKeyBlobClockSkew. A listing without the probe is an error too: the run
// cannot know the clock of the store.
func checkProbeClock(objects []blob.Object, probe blob.Key, now time.Time) error {
	for _, obj := range objects {
		if obj.Key != probe {
			continue
		}

		diff := obj.ModifiedAt.Sub(now)
		if obj.ModifiedAt.IsZero() || diff > ItemKeyBlobClockSkew || diff < -ItemKeyBlobClockSkew {
			return fmt.Errorf("%w: the store clock is %s from the server clock, the limit is %s",
				ErrItemKeyBlobClockSkew, diff.Round(time.Second), ItemKeyBlobClockSkew)
		}

		return nil
	}

	return fmt.Errorf("%w: the listing does not hold the clock probe", ErrItemKeyBlobClockSkew)
}

// garbageRun is one run of the reaper rule in the namespace ns at the time
// now. probe is the probe blob of the run, which is never garbage.
type garbageRun struct {
	named ItemKeyBlobNamer
	now   time.Time
	ns    namespace.ID
	probe blob.Key
}

// filter returns the keys of objects that are garbage, with the rule of
// Garbage. It is the one place of that rule, and it does not rely on the view
// that listed objects.
func (g garbageRun) filter(ctx context.Context, objects []blob.Object) ([]blob.Key, error) {
	// The clock check comes first, for the whole listing: a store time far
	// ahead of the server shows that the two clocks are not in step, so no
	// age in the listing is certain, and no key of this run is garbage.
	for i := range objects {
		if objects[i].ModifiedAt.After(g.now.Add(ItemKeyBlobClockSkew)) {
			return nil, fmt.Errorf("%w: a blob is %s ahead, the limit is %s", ErrItemKeyBlobClockSkew,
				objects[i].ModifiedAt.Sub(g.now).Round(time.Second), ItemKeyBlobClockSkew)
		}
	}

	var garbage []blob.Key

	for i := range objects {
		isGarbage, err := g.isGarbage(ctx, objects[i])
		if err != nil {
			return nil, err
		}

		if isGarbage {
			garbage = append(garbage, objects[i].Key)
		}
	}

	return garbage, nil
}

// isGarbage reports whether obj is garbage in this run.
func (g garbageRun) isGarbage(ctx context.Context, obj blob.Object) (bool, error) {
	// The grace period comes first: a blob of a write that still commits is
	// never a candidate, whatever the records say now.
	if obj.ModifiedAt.IsZero() || g.now.Sub(obj.ModifiedAt) < ItemKeyBlobGracePeriod || obj.Key == g.probe {
		return false, nil
	}

	// A probe of an earlier run that a failed delete left. Only a probe of
	// this namespace counts.
	if isClockProbe(g.ns, obj.Key) {
		return true, nil
	}

	// Defense in depth: the view lists only under the item-keys directory of
	// ns. Each key is checked again, so a listing fault of an adapter never
	// gives the reaper a key of another namespace or another family.
	part, ok := itemKeyBlobOf(g.ns, obj.Key)
	if !ok {
		return false, nil
	}

	isNamed, err := g.named(ctx, part)
	if err != nil {
		return false, fmt.Errorf("read the records of item key blobs: %w", err)
	}

	return !isNamed, nil
}

// checkDigest compares the size and the MD5 of wrapped with want. On a
// mismatch it writes one refusal to the IntegrityRecorder and returns
// ErrItemKeyBlobMismatch, joined with the error of the recorder when the
// record fails, so the caller still sees the refusal.
//
// Defense in depth: the AEAD tag of the wrapped key refuses changed bytes and
// a key of another item too. This check stays so that a blob that a store
// replaced, for example with a wrapped key of the same item from another
// write, is refused before any unwrap, and so that the refusal is recorded.
func (b *ItemKeyBlobs) checkDigest(ctx context.Context, ns namespace.ID, wrapped []byte, want ItemKeyBlobInfo) error {
	got := blob.DigestBytes(wrapped)
	if got.Size == want.Size && got.MD5 == want.MD5 {
		return nil
	}

	// The record must survive a canceled request: the refusal shows a fault
	// of the store or an attack.
	if recErr := b.mismatches.RecordMismatch(context.WithoutCancel(ctx), ns, "get"); recErr != nil {
		return errors.Join(ErrItemKeyBlobMismatch, fmt.Errorf("record the mismatch: %w", recErr))
	}

	return ErrItemKeyBlobMismatch
}

// itemKeyBlobOf returns the parts of key when key is an item key blob key of
// the namespace ns, and false for any other key.
func itemKeyBlobOf(ns namespace.ID, key blob.Key) (ItemKeyBlob, bool) {
	part, err := ParseItemKeyBlobKey(key)

	return part, err == nil && part.Namespace == ns
}

// isClockProbe reports whether key is a probe blob of a reaper run in the
// namespace ns: namespaces/<ns>/item-keys/clock-probe-<20 letters and digits>.
func isClockProbe(ns namespace.ID, key blob.Key) bool {
	rest, ok := strings.CutPrefix(key.String(), "namespaces/"+ns.String()+"/"+itemKeyDir+clockProbePrefix)

	return ok && validAttempt(rest)
}

// view returns the view of the store for the namespace ns.
func (b *ItemKeyBlobs) view(ns namespace.ID) (*blob.Scoped, error) {
	v, err := blob.NewScoped(b.store, ns)
	if err != nil {
		return nil, fmt.Errorf("scope of item key blobs: %w", err)
	}

	return v, nil
}

// scopedKey returns the view of ns after it checks that key is a key of the
// item-keys directory of ns. A key of another namespace, or a key of another
// directory of ns, is a refusal.
//
// Defense in depth: the view refuses a key outside the namespace too. This
// check stays so that a key of a blob of another kind in the same namespace,
// for example a checkpoint, never reaches the item key code, and it does not
// rely on the view.
func (b *ItemKeyBlobs) scopedKey(ctx context.Context, ns namespace.ID, key blob.Key, op string) (*blob.Scoped, error) {
	v, err := b.view(ns)
	if err != nil {
		return nil, err
	}

	rest, ok := strings.CutPrefix(key.String(), v.Prefix()+itemKeyDir)
	if !ok {
		return nil, b.refuse(ctx, ns, op, blob.ErrOutOfScope, scopeaudit.PlaceOf(v, key))
	}

	if !validItemKeyRest(rest) {
		return nil, fmt.Errorf("%w: item key blob key", ErrInvalidItemRef)
	}

	return v, nil
}

// validItemKeyRest reports whether rest, the part of a key after the item-keys
// directory, is <item key ID>/v<version>-<attempt>.
func validItemKeyRest(rest string) bool {
	id, ver, ok := strings.Cut(rest, "/")
	if !ok {
		return false
	}

	if _, err := ParseItemKeyID(id); err != nil {
		return false
	}

	_, _, ok = parseItemKeyVersion(ver)

	return ok
}

// refuse writes the refusal to the recorder when err is blob.ErrOutOfScope. It
// returns err, joined with the error of the recorder when the record fails, so
// the caller still sees the refusal. Any other error passes through.
func (b *ItemKeyBlobs) refuse(
	ctx context.Context, ns namespace.ID, op string, err error, where scopeaudit.Where,
) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	if recErr := b.refusals.RecordOutOfScope(ctx, ns, op, where); recErr != nil {
		return errors.Join(blob.ErrOutOfScope, fmt.Errorf("record the refusal: %w", recErr))
	}

	return blob.ErrOutOfScope
}

// mapItemKeyBlobError translates a missing blob into ErrKeyNotFound, as the
// ItemSealer documents.
func mapItemKeyBlobError(err error) error {
	if errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("item key blob: %w", ErrKeyNotFound)
	}

	return fmt.Errorf("get item key blob: %w", err)
}
