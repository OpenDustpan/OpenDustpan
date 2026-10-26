// Package blobrefs turns the rows that name a blob outside SQL into the blob
// references of a backup: the blobs of job plans, of job log chunks and of
// registry releases. The SQLite and PostgreSQL adapters read the rows in their
// own dialect and give them to a Collector, so both build the same keys and
// apply the same rules.
//
// A row that names a blob without the size and the MD5 of its stored bytes
// (a row from an earlier version of the server) gives no reference. Its key
// goes to the list of blobs that the backup does not copy, so that the
// manifest reports it. The backup never copies a blob that it cannot check.
//
// The rows are untrusted: a restore or another version of the server can have
// written them. Every ID is checked against an allowlist before it becomes
// part of a key.
package blobrefs

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// ErrRow reports a row that names a blob in a way that no server writes: an ID
// outside the allowlist or a plan document that does not decode. It matches
// backup.ErrCorrupt.
var ErrRow = fmt.Errorf("blob reference row is not valid: %w", backup.ErrCorrupt)

// md5Size is the size of an MD5 in bytes.
const md5Size = 16

// idRE is the allowlist of an ID that becomes a part of a key: lowercase
// letters, digits and dashes.
var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

// ChunkRow is one row of the log chunk table, joined with the job of its log
// version and with the item key of that version.
type ChunkRow struct {
	NamespaceID string
	JobID       string
	LogID       string
	Seq         int64
	Confirmed   bool
	// Shredded is true when a redaction shredded the item key of the log
	// version: the redaction deleted the blob, so no blob is left to copy.
	Shredded   bool
	StoredSize int64
	StoredMD5  []byte
}

// PlanRow is one row of the plan table, with the item key of the plan.
type PlanRow struct {
	NamespaceID string
	JobID       string
	ID          string
	Body        string
	Shredded    bool
}

// ReleaseRow is one row of the registry release table. Release is the
// release that registrystore.Release built from the row, and NamespaceID is
// the stable ID of the namespace, which the content keys use.
type ReleaseRow struct {
	NamespaceID string
	Release     registry.Release
}

// Collector gathers the references of a backup. The zero value is ready.
//
// The collector keeps one set of the keys it added and one of the keys it listed
// as not copied, so that a backup of a million chunks does not compare each key
// with every earlier key.
type Collector struct {
	refs       []backup.BlobRef
	added      map[string]struct{}
	unverified map[string]struct{}
}

// Refs returns the references that the backup copies, in the order added.
func (c *Collector) Refs() []backup.BlobRef {
	return slices.Clone(c.refs)
}

// Unverified returns the keys that rows name and that the backup does not
// copy, because the rows hold no size and MD5 of the stored bytes. It is
// sorted and has no key twice.
func (c *Collector) Unverified() []string {
	out := make([]string, 0, len(c.unverified))
	for key := range c.unverified {
		out = append(out, key)
	}

	slices.Sort(out)

	return out
}

// Chunk adds the blob of a log chunk. A chunk that no writer confirmed has no
// blob that a reader counts. A chunk of a shredded log version has no blob.
func (c *Collector) Chunk(row ChunkRow) error {
	if !row.Confirmed || row.Shredded {
		return nil
	}

	if !idRE.MatchString(row.NamespaceID) || !idRE.MatchString(row.JobID) || !idRE.MatchString(row.LogID) ||
		row.Seq < 0 || row.Seq >= job.MaxLogChunks {
		return fmt.Errorf("%w: log chunk %d of log %q", ErrRow, row.Seq, row.LogID)
	}

	rel, err := job.LogChunkKey(job.ID(row.JobID), row.LogID, int(row.Seq))
	if err != nil {
		return fmt.Errorf("%w: log chunk key: %w", ErrRow, err)
	}

	key, err := scopedKey(row.NamespaceID, rel.String())
	if err != nil {
		return err
	}

	c.add(key, row.StoredSize, row.StoredMD5)

	return nil
}

// Plan adds the blob of each part of a stored plan. A reserved plan has no
// part, and a redacted plan or a plan of a shredded key has no blob.
func (c *Collector) Plan(row PlanRow) error {
	if row.Shredded {
		return nil
	}

	if !idRE.MatchString(row.NamespaceID) || !idRE.MatchString(row.JobID) || !idRE.MatchString(row.ID) {
		return fmt.Errorf("%w: plan %q", ErrRow, row.ID)
	}

	plan, err := jobstore.ParsePlan(row.ID, row.Body)
	if err != nil {
		return fmt.Errorf("%w: plan %q: %w", ErrRow, row.ID, err)
	}

	if !plan.RedactedAt.IsZero() {
		return nil
	}

	for _, part := range plan.Parts {
		rel, keyErr := job.PlanPartKey(job.ID(row.JobID), row.ID, part.Kind)
		if keyErr != nil {
			return fmt.Errorf("%w: plan part key: %w", ErrRow, keyErr)
		}

		key, scopeErr := scopedKey(row.NamespaceID, rel.String())
		if scopeErr != nil {
			return scopeErr
		}

		sum, ok := parseMD5(part.StoredMD5)
		if !ok {
			c.add(key, part.StoredSize, nil)

			continue
		}

		c.add(key, part.StoredSize, sum[:])
	}

	return nil
}

// Release adds the blob of each file of a registry release, once for each
// content hash.
func (c *Collector) Release(row ReleaseRow) error {
	if !idRE.MatchString(row.NamespaceID) {
		return fmt.Errorf("%w: registry release in namespace %q", ErrRow, row.NamespaceID)
	}

	for _, hash := range row.Release.FileHashes() {
		key, err := registry.BlobKey(namespace.ID(row.NamespaceID), hash)
		if err != nil {
			return fmt.Errorf("%w: registry file key: %w", ErrRow, err)
		}

		i := slices.IndexFunc(row.Release.Files, func(f registry.StoredFile) bool { return f.SHA256 == hash })
		if i < 0 {
			c.list(key.String())

			continue
		}

		sum, ok := parseMD5(row.Release.Files[i].MD5)
		if !ok {
			return fmt.Errorf("%w: registry file %q has an MD5 that is not valid", ErrRow, hash)
		}

		c.add(key.String(), row.Release.Files[i].Size, sum[:])
	}

	return nil
}

// list adds key to the keys that the backup does not copy.
func (c *Collector) list(key string) {
	if c.unverified == nil {
		c.unverified = make(map[string]struct{})
	}

	c.unverified[key] = struct{}{}
}

// add adds a reference when the size and the MD5 are those of stored bytes,
// and lists the key as not copied otherwise. A key that two rows name once is
// added once: the content is the same, because the key follows the content for
// a registry file, and a chunk or a plan part belongs to one row.
func (c *Collector) add(key string, size int64, md5sum []byte) {
	if size <= 0 || len(md5sum) != md5Size {
		c.list(key)

		return
	}

	if _, dup := c.added[key]; dup {
		return
	}

	if c.added == nil {
		c.added = make(map[string]struct{})
	}

	c.added[key] = struct{}{}

	var sum [md5Size]byte

	copy(sum[:], md5sum)

	c.refs = append(c.refs, backup.BlobRef{MD5: sum, Key: key, Size: size, Sealed: false})
}

// scopedKey returns the key of rel inside the namespace with the given ID.
func scopedKey(nsID, rel string) (string, error) {
	key, err := blob.ParseKey("namespaces/" + nsID + "/" + rel)
	if err != nil {
		return "", fmt.Errorf("%w: key: %w", ErrRow, err)
	}

	return key.String(), nil
}

// parseMD5 reads an MD5 in lowercase hexadecimal.
func parseMD5(s string) ([md5Size]byte, bool) {
	var out [md5Size]byte

	if !job.ValidMD5(s) {
		return out, false
	}

	b, err := hex.DecodeString(s)
	if err != nil || len(b) != md5Size {
		return out, false
	}

	copy(out[:], b)

	return out, true
}
