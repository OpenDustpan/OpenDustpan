# 243. The source port, its adapters and their conformance suites

Status: accepted · 2026-10-19. Part of ADR 241

## Context

ADR 241 makes an import a resource that the server runs, and ADR 242 says
what it moves for each tool. The owner named the first sources: Pulumi
Cloud (works with the Pulumi CLI), HCP Terraform and TFE-compatible
servers, S3 and other OpenTofu backends (ADR 241 decision 1).

CLAUDE.md asks for small ports in the package that consumes them, each with
its errors and a conformance suite, and for a conformance test before any
adapter implements a new port method. RFC 42 asked for each source as an
adapter behind a port, so that the community can add sources. ADR 12 asks
that shared packages stay tool-neutral, and that each tool keeps its own
model.

Facts:

- `pkg/blob/s3` signs its requests with AWS Signature Version 4 in its own
  code, with no SDK. No AWS, Google Cloud or Azure SDK is a dependency of
  the server.
- `internal/api/pulumi` and `internal/api/tfe` serve the Pulumi
  Cloud-compatible API and the TFE-compatible API. So an OpenDustpan server
  can be the source of a conformance test and of an e2e step.
- `history.Unit` names a unit in shared code by namespace, tool and ID.

## Decision

### The package and its ports

1. **A domain package `pkg/unitimport`.** The package is tool-neutral. It
   holds the import object, its states and transition table, the move
   check report (ADR 244), and four ports: `Source`, `Tool`, `Store` and
   `UnitStore`.
   Its name is not `import`, a Go keyword, and not `source`, the package of
   source snapshots (ADR 135). depguard treats its top-level files as domain
   code. Each adapter is in its own package below it.
2. **The `Source` port.** A source adapter reads one kind of source. Its
   methods, each with the credential of ADR 241 decision 4:
   - `Kind` answers the source kind.
   - `Units` lists the units of the source that a selection names.
   - `Versions` lists the versions of one unit, oldest first, after a
     version ID, at most a limit. The core pages with it.
   - `Open` streams the bytes of one version.
   - `Head` answers the ID of the newest version of one unit, for the
     writer check (ADR 241 decision 14).

   Each version carries its ID, its time, its size, the SHA-256 of its
   bytes, the source actor as text, and a metadata document. The metadata
   document is the tool part of a version, for example the update kind and
   message of a Pulumi update, or the serial and lineage of a state
   version. The core never parses it. It passes it to the `Tool` port.

   The port returns these errors, wrapped: `ErrSourceNotFound` (no such
   unit or version), `ErrSourceDenied` (the source refused the
   credential), `ErrSourceUnreachable` (a network error, a timeout or a 5xx
   answer), `ErrSourceInvalid` (an answer that the adapter cannot parse)
   and `ErrUnsupported`. An error text never holds the credential, a query
   of a URL or a byte of state.
3. **Two optional capabilities.** A source adapter can also implement:
   - `Decrypter`: decrypt a batch of ciphertexts of one unit through the
     source (ADR 242 decision 8). The Pulumi Cloud-compatible adapter
     implements it.
   - `Locker`: lock and unlock one unit in the source (ADR 241 decision
     13). The TFE-compatible adapter implements it.

   The plan of an import says which capabilities the source has. An option
   that needs a capability that the source does not have fails the dry run
   with `import.source-capability-missing`.
4. **The `Tool` port.** Each tool package (`stack`, `workspace`)
   implements it, and `pkg/unitimport` consumes it. So the core never reads
   a checkpoint or a state file. Its methods:
   - `Tool` answers the tool name of its units.
   - `Create` creates the target unit with the manual lock of ADR 241
     decision 12, in one fenced transaction.
   - `Write` writes one source version as one history item, with its times
     and its source fields (ADR 242 decision 3), its ledger row, and the
     inventory publish, in one fenced transaction. It takes the decrypt
     function of the source when the source has one.
   - `Facts` reads the facts of the move check (ADR 244 decision 3) from the
     bytes of a version. `LatestFacts` reads them from the latest state item
     of a target unit. Both use the same code, so the same bytes give the
     same facts on both sides.
5. **The `Store` and `UnitStore` ports.** `Store` keeps imports and their
   plans. `UnitStore` keeps the import units and reads their ledgers. Each
   applies each transition of the table in a fenced transaction. A
   transition that the table does not allow returns `ErrTransition`.
   `Tool.Write` writes the ledger rows in its own transaction, because the
   SQL adapter serves every port from one database (the rule of
   `namespace.Store`). Two ports keep each interface at most eight methods,
   the limit of the `interfacebloat` linter.

### Conformance

6. **One suite for each port, first.** `pkg/unitimport/unitimporttest`
   holds three suites. A new adapter runs the suite of its port. A new port
   method gets its tests in the suite before an adapter implements it.
   - **The source suite** takes a fixture: an adapter, a credential, a bad
     credential, and a seed function that puts known versions in the
     source. It checks the order of versions, the pages and the cursor, the
     SHA-256 of the bytes of each version, `Head` after a new version, each
     error of decision 2 (with a bad credential, a missing unit and a server
     that does not answer), the cancel of a context, a body above the size
     limit, and a canary credential that must not reach an error text or a
     log line. When the adapter implements `Decrypter` or `Locker`, the
     suite checks them too.
   - **The tool suite** checks `Create`, `Write` with each source field,
     the fence error, a crash between the blob and the row (no ledger row,
     no item), a second write of the same version, and that `Facts` of the
     source bytes equals `LatestFacts` after the write.
   - **The store suite** checks each transition of the table and the fence,
     for `Store` and `UnitStore`.

   The goroutine check of ADR 36 runs in each package of the suites.

   Benchmark: the conformance suites of `blob/blobtest` and
   `store/storetest` in this repository. The Go `testing/fstest.TestFS`
   checks any `fs.FS` with one call.
7. **The upload source is part of the core.** The upload source is not a
   network adapter. Its versions are the bodies of the upload route (ADR 241
   decision 21), in the order of their sequence numbers. The server streams
   each body to the blob store (ADR 45), with the state size limit of ADR 26
   decision 4. It runs the source suite with a fixture that uploads.

### The first adapters

8. **The order of the first adapters (product manager).** Each adapter
   lands with its e2e step first (ADR 244 decision 11):
   1. `tfe-api`: a TFE-compatible API. It reads the state versions of each
      workspace of an organization, and their `json-state-outputs` and
      `json-state` documents where the source gives them. It works with HCP
      Terraform, Terraform Enterprise and OpenDustpan as sources. Its
      capability is `Locker`.
   2. `pulumi-service`: a Pulumi Cloud-compatible API. It reads the stacks
      of an organization, the update list and each checkpoint version. It
      works with Pulumi Cloud and OpenDustpan as sources. Its capability
      is `Decrypter`.
   3. `s3`: an S3-compatible bucket. It reads OpenTofu state keys with
      their object versions, and the layout of a Pulumi DIY backend (ADR
      242 decisions 6 and 13). It signs with the Signature Version 4 code
      of `pkg/blob/s3`, which moves to a shared internal package.
   4. `opentofu-http`: the OpenTofu `http` backend, the current state only.

   The TFE-compatible adapter comes first, because ADR 29 already has its
   replay as a script and its e2e step. The Pulumi Cloud-compatible adapter
   comes next, because it is the one source where only the server can keep
   the history and re-encrypt the secrets at once.

   Benchmark: the order of RFC 42 ("the gap that only OpenDustpan fills")
   and the e2e steps of ADR 29 that already exist.
9. **Source kinds name a protocol, not a product (product manager).** The
   API value of a source kind names the protocol that the adapter speaks
   (`tfe-api`, `pulumi-service`, `s3`, `opentofu-http`, `upload`). The docs
   page names the products that each kind works with, in the words of
   CLAUDE.md: "works with the Pulumi CLI", "a TFE-compatible API". The
   console shows the product names only as "works with" text.

   Benchmark: the `backend "s3"` and `backend "http"` blocks of OpenTofu
   name a protocol. The `cloud` block of OpenTofu works with more than one
   product.
10. **No new dependency (product manager).** The first adapters use the
    standard library and the code of this repository. A source that needs an
    SDK (for example GCS or Azure Blob) imports through the upload source
    until a later decision adds its adapter. The project asks the owner
    before it adds a dependency (CLAUDE.md).

    Benchmark: ADR 60 decision 12 builds the OTLP sink with the standard
    library, with no dependency.
11. **Bounds of a source request.** Each adapter sends its requests through
    `egresshttp` (ADR 241 decision 6). It reads at most the state size
    limit for one version, and at most `egress.MaxBodyBytes` for one other
    answer. One import sends at most 4 requests at a time to its source. On
    429 or 503, an adapter waits for the `Retry-After` of the answer, at
    most 60 seconds, then uses the back-off of `pkg/durable`. A request
    that a person cannot see in the source (a decrypt, a lock) is in the
    plan of the import.
12. **An SDK user can add a source.** `dustpan.NewServer` (ADR 35) takes an
    option that registers a source kind and the function that opens its
    adapter. An adapter outside this repository runs the source suite of
    decision 6.

## Consequences

- The Signature Version 4 code of `pkg/blob/s3` moves to an internal package
  that both the blob adapter and the S3 source adapter use. The blob adapter
  keeps its tests.
- Each adapter follows a protocol that another project owns and can change.
  The source suite runs against an OpenDustpan server in this repository,
  and the e2e steps of ADR 244 drive the real CLIs. Neither tests Pulumi
  Cloud or HCP Terraform. The docs page says so (ADR 244 decision 12).
- The `stack` and `workspace` packages each implement `unitimport.Tool`.
  This is the first port that a tool package implements for a core
  package, apart from the inventory publish of ADR 13.
- `pkg/unitimport` starts with its types and its ports. The transition
  table, the suites and the adapters come in the order of ADR 241.
