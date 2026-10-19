# 240. A state download by a person is a reveal, and a principal has a kind

Status: accepted · 2026-10-18. Supersedes in part ADR 41 decisions 9 and 13 and ADR 60 decision 4. Extends ADR 41 decision 5 and ADR 226 decision 2

## Context

The console design for the OpenTofu workspace page
(`OpenDustpan/console`, `docs/design/opentofu-workspace-page.md`, section
3.2, gap G7) lets a person download a state version. ADR 239 lists the
other gaps.

The download `GET /api/v2/state-versions/{id}/download` needs `unit:read`
and `unit:read-secrets`. It answers the whole state file, with plain secret
values. ADR 41 decision 9 makes each download write an audit entry of the
kind `tool-operation`, with an empty value path. ADR 41 decision 13 gives
the reason: the server cannot see who runs a CLI command, so a person who
runs `tofu state pull` makes a `tool-operation` entry.

The kind `tool-operation` exists so that a reader never confuses a CLI read
with a person who asked to see secret values. A download from the console
is a person who asked to see secret values. With the rule of ADR 41, it
writes a `tool-operation` entry.

The client declares the channel in the header `Dustpan-Channel` (ADR 41
decision 13). Any client can send `Dustpan-Channel: console`. So the
channel cannot decide the kind.

Facts from `main`:

- `principal.Kind` has three values: `person`, `service` and `workload`
  (ADR 52 decision 1). The principal record holds the kind. No route
  changes the kind after the server makes the record: an update changes the display
  name only.
- ADR 226 decision 2 names the fact `principal.kind` with the values
  `person`, `service` and `agent`, and the fact `principal.credential`
  with the values `session`, `access-token`, `job-token` and
  `trust-token`. The record has no kind `agent`, and ADR 226 does not name
  `workload`. An agent acts as the user who asked it, through an act-as
  edge (ADR 100 decision 17, ADR 147).
- An access token has the kind `personal` or `service`. The kind comes
  from the kind of its principal (`accesstoken.KindFor`, ADR 53 decisions
  7 and 10). A workload gets no access token. It uses a trust rule (ADR
  54).
- The access token lookup and the session lookup read the owner with its
  kind. The session service refuses a session whose owner is not a person.
- `identity.Principal`, which each route gets, has no kind. It has
  `Subject`, `DisplayName`, `Admin`, `TokenID` and `TokenExpiresAt`.
- The bootstrap admin token (ADR 6) has no stored record. ADR 53 decision
  9 retires it on the first sign-in.
- A job token is an access token of the principal that started the job
  (ADR 100 decision 20). The audit entry has no field for the job ID.

A decision of the product manager has the label "(product manager)" and a
"Benchmark:" line. The product manager decided under the owner's rule:
follow what Google, AWS, PCI DSS, SOC 2 and ISO 27001 would do.

## Decision

1. **A download by a person is a reveal (product manager).** A state
   download by a principal of the kind `person` writes an audit entry of
   the kind `reveal`. Its value path is `""`, the JSON Pointer of the whole
   state document (RFC 6901). A download by any other principal writes the
   kind `tool-operation`, as before. The principal decides the kind, with
   the two fields of decision 3: `reveal` when `Credential` is `session`
   or `access-token` and `Kind` is `person` or empty (decision 4).
   `tool-operation` in all other cases. An empty `Credential` counts as
   `access-token`, for the same reason as decision 4. The `Dustpan-Channel` header never
   decides it.

   Benchmark: SOC 2 CC7.2 and ISO/IEC 27001:2022 Annex A 8.15 ask for a
   log of access to sensitive data that names who acted. A person who
   reads every secret value of a state is that access. This decision is
   designed against those controls. It makes no compliance claim.
2. **The channel stays a declared field.** The entry records the channel
   as the client declares it (ADR 41 decision 13, ADR 60 decision 5). A
   reader can see `reveal` with the channel `api`, for example a person
   who runs `tofu state pull` with a personal token. A reader can see
   `tool-operation` with the channel `console`, for example a service
   token that sends the console header.
3. **A principal has a kind and a credential on each request (product
   manager).** `identity.Principal` gets two fields:
   - `Kind`: the kind of the principal record of ADR 52 decision 1, with
     exactly the values of the code: `person`, `service` and `workload`.
     The empty value is an unknown kind.
   - `Credential`: the kind of the credential of the request, with the
     values of ADR 226 decision 2: `session`, `access-token`, `job-token`
     and `trust-token`.

   Each authenticator fills both from the record that it already reads.
   It adds no store read:

   | Credential | Kind |
   | --- | --- |
   | A console session | `person` (a session belongs to one person), with the credential `session` |
   | A personal token | `person`, with the credential `access-token` |
   | A service token | `service`, with the credential `access-token` |
   | A token from a trust rule (ADR 54) | `workload`, with the credential `trust-token` |
   | A job token (ADR 100) | the kind of the principal that started the job, with the credential `job-token`. A download with it writes `tool-operation` (decision 1) |
   | A break-glass account session or token | `person` (ADR 70). `principal.Validate` refuses another kind |
   | The bootstrap admin token | empty: it has no record. Decision 4 counts it as a person. Its credential is empty, and decision 1 counts it as `access-token` |

   The record decides the kind. The credential decides whether a person
   acts through it. A job token and a trust token never make a `reveal`
   entry. A test checks each row of the table.

   This decision also extends ADR 226 decision 2. The fact
   `principal.kind` takes the same record kinds: `person`, `service` and
   `workload`. The fact `principal.credential` reads `Credential`. The
   value `agent` of ADR 226 is not a record kind. An agent acts as its
   user, so `principal.kind` is the kind of that user, and a gate tells an
   agent apart through `source.kind == "agent"`.

   Benchmark: AWS IAM keeps the principal type apart from the credential
   (a session or an access key), and AWS CloudTrail records both.
4. **An unknown kind counts as a person (product manager).** A principal
   with the empty kind writes `reveal`. When the server cannot name the
   kind, it writes the stricter entry (fail closed). This holds for the
   bootstrap admin token and for each authenticator outside this
   repository (ADR 35) that does not fill the field. Such an authenticator
   fills `service` or `workload` to keep its downloads `tool-operation`.
   The embedder sets this value, so the embedder is responsible for it.

   Benchmark: AWS CloudTrail never records the root user as a service
   identity. It has its own type, `Root`.
5. **The entry records the kind.** An audit entry gets the optional field
   `principalKind` (ADR 60 decision 5). A reader can then see why an entry
   has its kind of access. An entry from a principal with no kind has no
   `principalKind`. Its kind of access follows decision 4. An entry made
   with a job token also gets the optional field `jobID`, the job-ID claim
   of the token (ADR 100 decision 20). An older entry keeps its hash.
6. **Other kinds stay.** The reveal call of ADR 41 decision 4 and the two
   audit routes of ADR 23 keep the kind `reveal`. The Pulumi `decrypt`
   and `batch-decrypt` routes and the OpenTofu route for one sensitive
   output keep `tool-operation`. A later change can apply decision 1 to
   them. This ADR does not decide that.
7. **The freshness window.** A download that writes `reveal` does not fall
   under the window of fresh authentication (ADR 56, ADR 227) now:
   - A download with an access token never needs a fresh authentication.
     An access token never gives one (ADR 227 decision 6). A step would
     refuse each `tofu plan` with a personal token.
   - The TFE-compatible API accepts only an access token today. So no
     download can carry a fresh authentication now.
   - Whether a download with a console session needs a fresh
     authentication cannot be decided before the TFE-compatible API
     accepts the session. RFC 80 holds that question, together with the
     force-unlock. The direction of the product manager is that both need
     it once the console signs in with a session.
8. **Fail closed stays.** The server writes the entry before the first
   byte of the download. When it cannot write the entry, the download
   answers 500 with no content (ADR 41 decision 5).

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it
replaces:

- **ADR 41, decision 9:** "each call writes an audit entry of the kind
  `tool-operation`, with an empty value path for a download". For a
  download by a person, the kind is `reveal` (decision 1). The single
  output route does not change.
- **ADR 41, decision 13:** "The server cannot see who runs a CLI command.
  So a person who runs `tofu state pull` makes an entry of the kind
  `tool-operation`." The server now sees the kind of the principal. A
  person with a personal token makes a `reveal` entry. A person who uses a
  service token still makes a `tool-operation` entry.
- **ADR 41, decision 13:** "`reveal`: ... The reveal call of decision 4
  and the two audit routes of ADR 23 write this kind." A download by a
  person writes this kind too.
- **ADR 60, decision 4:** "A route that already writes `tool-operation`,
  `reveal` or `override` keeps that kind." The download route writes one
  of two kinds, by the principal.
- **ADR 226, decision 2:** "`kind` (`person`, `service` or `agent`)" and
  "An agent is a subject of gates through `source.kind == "agent"` and
  `principal.kind == "agent"`." The fact `principal.kind` takes the record
  kinds `person`, `service` and `workload` (decision 3). An agent is a
  subject of gates through `source.kind == "agent"`.

## Consequences

- `identity.Principal` gets two fields. The access token service, the
  session check and the job token authenticator fill them. The trust-rule
  exchange of ADR 54 is not built. The token it issues authenticates a
  principal of the kind `workload`, and the exchange fills the fields when
  it lands. The `identitytest` suite gets a case for the empty kind.
- The kind needs no migration. The token lookup and the session
  lookup already join the owner with its kind, and the kind cannot change
  after the server makes the record.
- The audit entry gets `principalKind` and `jobID`. The SQL stores get a numbered
  migration for the columns. The `audittest` suite checks that an older
  entry keeps its hash.
- An installation where people run `tofu plan` with personal tokens gets
  a `reveal` entry for each state download of each plan. A
  `terraform_remote_state` read of another workspace with a personal token
  also writes one `reveal` entry, so one plan can write more than one. The
  audit log
  guide says this, and says that a service token or a workload identity
  for CI keeps CI reads as `tool-operation`.
- A person who can make a service token, or start a job, can read a
  state as `tool-operation`. The token creation entry and the job record
  name that person. A `tool-operation` download with a job token records
  the job ID (ADR 100 decision 20).
- The e2e audit steps for OpenTofu downloads and remote-state reads, which
  use the bootstrap admin token, expect `reveal`. A new step with a
  service token proves `tool-operation`. Both land with their e2e step
  first (ADR 12).
- The status line of ADR 226 names this ADR. Its text does not change
  (ADR 1).
- The glossary entry "Kind of access" changes. "Principal kind" is a new
  term.
- The audit log guide and the security model guide change in the same
  change (ADR 214).
