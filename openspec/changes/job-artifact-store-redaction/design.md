## Context

The server must redact a secret from a job log, and prove it. The port `job.ArtifactStore` and its rules exist. Its store does not, so the route answers `job.unsupported` (see the proposal for the proof). This design adds the store, the two SQL tables of the redaction log, the record shape for earlier versions, the wiring and the action family.

## Decisions

1. **One adapter over existing ports.** `job.ArtifactStores` implements `job.ArtifactStore` over `RecordStore`, a new `job.RedactionStore`, `ArtifactBlobs`, `secret.ItemSealer` and `namespace` reads. It adds no method to `blob.Store`. Why no structure removes the need for the checks: the rules `CheckPlanRedaction` and `CheckLogRedaction` already are the one place of each rule. The adapter calls them and has no copy.
2. **The transaction lives in the SQL store.** The mark, the key shred, the redaction record and the audit entry need one transaction, so `RedactionStore` is a port of the job store with two methods. The adapter cannot make the transaction from smaller ports, because `ShredItemKey`, `AppendRedaction` and the audit append would be three transactions.
3. **Earlier versions live in the step record.** `StepRecord.Earlier []LogRef` holds the older versions, oldest first, and `LogRef.Replaces` holds the ID of the version that it replaced. One function, `job.LogVersionOf(step, id)`, searches `Log` and `Earlier`. `logOfRow` and `service_redact.go` both call it, so the two cannot disagree again. `job_logs` already holds one row for each version.
4. **Write order follows the recorder.** `ReplaceLog` writes the item key blob, then the sealed chunks (each at most `MaxLogChunk`), then the SQL record and the key record in one fenced transaction. A crash before the transaction leaves unnamed blobs, which the artifact sweep removes. The older version stays readable until `Redact` commits.
5. **The key is shredded at commit.** The sealed blobs of the older version are unreadable at once. Deleting them is the shred operation of the history model, a separate change. Each shred target stays `pending`, and `Redact` answers the targets as the port documents.
6. **The family.** `identity.ActionUnitRedact: FamilyData` goes into `families` in `pkg/access/access.go`. The authorizer denies an action without a family to every grant holder, so this one line makes the stored grant of `admin` count. No code defines who holds the action.
7. **Fail closed when a port is missing.** The wiring builds the store only when the blob store, the job store and the sealer exist. If one is missing, `Artifacts` stays nil and the route answers 501 `job.unsupported`, as today.

## Contract

Proposed Go signatures. Each error names the sentinel that the suite requires.

```go
// pkg/job
type ArtifactStores struct{ /* unexported */ }

func NewArtifactStore(c ArtifactStoreConfig) (*ArtifactStores, error)

type ArtifactStoreConfig struct {
	Records   RecordStore
	Redactions RedactionStore
	Blobs     *ArtifactBlobs
	Sealer    secret.ItemSealer
	Namespaces NamespaceReader
	Now       func() time.Time
}

// ArtifactStore (existing) is implemented in full:
//   Redact(ctx, history.Redaction, targets, audit.Entry, fence) (history.RedactionResult, error)
//   ReplaceLog(ctx, LogReplacement) (LogRef, error)
//   plus the methods of ArtifactReader.

// RedactionStore is the transactional side of the job store.
type RedactionStore interface {
	// PutLogVersion stores next as the current log of the step and moves
	// the current log to Earlier, in one fenced transaction with the key record.
	// Errors: namespace.ErrFenceStale, ErrNotFound, ErrWrongState, secret.ErrItemKeyExists.
	PutLogVersion(ctx context.Context, p LogVersionPut) (LogRef, error)
	// RedactItem marks the item redacted, shreds its key, appends the record
	// and the audit entry, in one fenced transaction.
	// Errors: namespace.ErrFenceStale, history.ErrRedacted, history.ErrInProgress,
	// history.ErrLatest, ErrNotFound.
	RedactItem(ctx context.Context, r history.Redaction, targets []history.Target,
		e audit.Entry, f namespace.Fence) (history.RedactionResult, error)
}

// pkg/job
type StepRecord struct { /* existing */ Earlier []LogRef }
type LogRef struct { /* existing */ Replaces ID }
func LogVersionOf(s StepRecord, id ID) (LogRef, bool)
```

Stored schema, in the baseline of each dialect, in name order, timestamps in Unix milliseconds in `BIGINT`:

| Table | Columns | Keys |
| --- | --- | --- |
| `redactions` | `id`, `namespace_id`, `unit_id`, `kind`, `item_id`, `cause`, `reason`, `actor`, `redacted_at_ms`, `audit_id` | primary key `id`; index on `namespace_id, item_id` |
| `redaction_targets` | `redaction_id`, `ordinal`, `kind`, `ref`, `state` (`pending`), `updated_at_ms` | primary key `redaction_id, ordinal`; foreign key to `redactions` |

The `reason` column holds the reason of the person. No column holds the text that the person gave. The two tables join the portable backup list.

HTTP: no new route. The route `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/log-redactions` of the change `add-job-service-routes-start-1` keeps its request and response. Its 404 code is `job.not-found`. The job read shows `earlier` for each step, in the same JSON shape as `log`, time fields in Unix milliseconds named `...At` as the handler writes them today.

## Layers (ADR 250)

| Layer | What it does | Hostile test, sent to that layer alone |
| --- | --- | --- |
| Boundary (handler) | Rejects an empty match, a match over the size limit and a version ID that is not an ID. | Empty, huge and malformed IDs to the handler. |
| Service | Resolves the version through `LogVersionOf`, calls `CheckLogRedaction`, reads at most `MaxRedactLogBytes`. | A version of another job, of another namespace, a running step, the current version. |
| Adapter | Takes the namespace ID from the resolved row, builds each blob key through the scope, checks the fence. | A fence of an old owner, a version row of another namespace, a blob key with `..`. |
| Store (SQL) | One transaction, placeholders only, the fence check in the transaction. | A failed audit insert in the middle: no row changes. |
| Authorization | `unit:redact` on the unit, checked at the route and again in the service. | A `writer`, a `viewer`, `job:read-logs` only. Each fails when the family line is removed. |
| Fail closed | A missing port gives `job.unsupported`. A decode error of a record gives an error, not an empty version. | A store with no blob store wired. |

## Threat model

- Text of the match in the audit entry, a log line or an error: the entry holds the count, the hashes and the reason only. A test searches every audit field, the record JSON and the error text for the match.
- A caller picks a version of another namespace: the adapter takes the namespace ID from the row it resolved, never from the request.
- A second instance reads the shredded version from a cache: `ErrShredded` comes from the item key index, which every instance reads. The test reads through a second sealer.
- A crash between `ReplaceLog` and `Redact`: the retry rule (decision 3) redacts only the older version.
- A very large log: the service reads at most `MaxRedactLogBytes`, the adapter holds one chunk.
- Replay of one request: the second call finds the older version redacted and answers 409.

## Failure modes

| Failure | Effect | Recovery |
| --- | --- | --- |
| Blob write fails in `ReplaceLog` | No record changes | The caller retries. The sweep removes unnamed blobs. |
| Transaction fails in `PutLogVersion` | No new version | The caller retries. |
| `RedactItem` fails after `PutLogVersion` | Next version exists, older version readable | A retry redacts only the older version. |
| Fence moves | `namespace.ErrFenceStale`, nothing written | The caller reads the namespace again. |

## Risks

- The change is large. Group 2 (the SQL redaction log) can land first as its own pull request, because group 3 and the later groups depend on it and it depends on nothing else.
- The change depends on the archive of `add-job-service-routes-start-1`, because the delta `specs/job-actions` modifies requirements that the archive creates.
