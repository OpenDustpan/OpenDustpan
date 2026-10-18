# 163. The resource index and unmanaged resources found from state

Status: accepted · 2026-10-17

## Context

On 17 October 2026 the project owner decided:

- The server finds **unmanaged resources** of three kinds: not in any unit,
  left behind (a person deleted the unit) and managed twice (the state of two
  units holds the resource). `GLOSSARY.md` has the term and its kinds.
  "Orphan" means a stored file only.
- Facts about the cloud are a concern of the infrastructure-as-code tools.
  OpenDustpan ships no cloud adapter in the server for facts.
- Each tool keeps its own model (ADR 12, ADR 13). Cross-tool views read the
  inventory. Writes in a namespace carry the fence.

RFC 47 says that "left behind" and "managed twice" may need no runner: the
state of the units holds what they need. "Not in any unit" needs a sweep of
the cloud (ADR 164). RFC 40 says that the managed-twice kind can come from
the inventory and from reading state.

The owner named the feature **Sweep**. A **sweep** is the work that finds
unmanaged resources: a cloud sweep on a runner (ADR 164) or a state check
in the server (decision 10). `GLOSSARY.md` uses "retention sweep" today for
the task that purges deleted units. One word for two things breaks the rule
of one word for each concept, so this design asks for that task to become
the **retention expiry** (see "Requests to other designs"). The Go names of
`pkg/unmanaged` keep "scan" (`Scan`, `ScanStore`): they are internal.

## Decision

### The resource index

1. **A tool-neutral projection.** The **resource index** holds, for the
   latest state item of each unit, one entry for each managed resource: the
   unit, the key of the resource in its state (the URN or the address), the
   type, and its **resource keys**. A resource key is the provider and the
   ID that the cloud gives the resource: the ARN for AWS, the resource ID
   for Azure, the full resource name for Google Cloud. Two keys name the same
   cloud resource when they are equal after `unmanaged.NormalizeKey`.
2. **Normal form.** An Azure ID is in lowercase with no trailing slash,
   because Azure compares resource IDs with no case. Other IDs keep their
   case. A key that does not have the form of its known provider is
   refused. A provider that the server does not know keeps its ID as it is.
   A table-driven test holds the rules.
3. **The key rules of a resource.** `unmanaged.KeysOf` gives the keys of one
   resource from its masked, tool-neutral view (`StateResource`):
   - `aws`: the attribute `arn`,
   - `azure`: the ID of the resource,
   - `gcp`: the attribute `full_resource_name` or `fullResourceName`, where
     the provider gives it.

   The adapter of each tool detects the provider from the type of the
   resource, for example `aws:s3/bucket:Bucket` and `aws_s3_bucket`, and
   gives only plain string attributes. A secret value never reaches the
   index.
4. **Data sources are not entries.** An OpenTofu `data` resource and a Pulumi
   resource that the program reads and does not manage are never entries. A
   read is not management, so it never makes a resource managed twice.
5. **When the index changes.** After each new latest state item of a unit,
   the durable operation that also updates the context projection (ADR 160
   decision 8) replaces the entries of the unit (`Index.ReplaceUnit`). The
   index keeps the entries of the item with the highest order. The
   operation has one start key for each unit, so the state items that
   arrive while it waits or runs **coalesce**: one run reads the latest item
   and replaces the entries one time. A unit that writes ten state items in
   a minute costs one read, not ten. The SQL adapter writes the
   **difference**: it deletes the entries that the new item no longer holds, inserts the new
   ones and keeps the others. A state item that changes one resource of a
   unit with 100,000 entries writes one row. The soft
   delete and the restore of a unit mark its entries as deleted or live in
   their own transaction (`Index.SetDeleted`). A purge deletes them
   (`Index.DropUnit`).
6. **Client-encrypted state.** The server cannot read such a state (ADR 41
   decision 8). Its unit has no entries. The console says that the index
   does not cover the unit.
7. **Bounds.** A unit has at most 100,000 entries. A unit above it indexes the
   first entries by resource key, and the inventory marks the unit as not
   fully indexed.

### Classification

8. **One rule for each key.** `unmanaged.Classify` gives the class of one
   resource key in one namespace, from its **holders** (the units of the
   namespace and of the linked namespaces whose latest state holds the
   key, decision 9) and whether a sweep saw it. It drops a holder of any
   other namespace. In order:
   1. Two or more distinct live units hold the key, and one of them is in the
      namespace: **managed twice**. Two resources of one unit are one holder.
   2. Exactly one live unit of the namespace holds the key: **managed**.
   3. Live units hold the key, none in the namespace: **elsewhere**.
   4. Only deleted units of the namespace hold the key: **left behind**.
   5. Only deleted units of linked namespaces hold the key: **elsewhere**.
   6. No unit holds the key, and a sweep saw it: **not in any unit**.
   7. Else: **none**.

   Managed, elsewhere and none make no finding. A table-driven test holds
   each case.
9. **Across namespaces, only with a link.** Two teams can use one cloud
   account from two namespaces. An installation administrator, with
   `server:link-namespaces` (new), **links** the two namespaces
   (`unmanaged.Links`). A link goes both ways. Without a link, the
   classification of a namespace reads the units of that namespace only:
   `Index.Holders` and `Index.Contested` never answer a holder of another
   namespace. The reasons:
   - **No enumeration.** A unit of a namespace cannot learn which keys the
     units of another namespace hold, for example by a state item that names
     guessed bucket names, or by a sweep report of guessed keys.
   - **No silent resolve.** A namespace cannot close the findings of
     another namespace by a state item that claims their resources. With no
     link, its unit is not a holder for the other namespace, so the finding
     stays open.
   - **No flood.** A namespace cannot open "managed twice" findings in
     another namespace by a copy of its keys.

   With a link, a resource that a unit of the other namespace manages is
   **elsewhere**: it is not a finding of this namespace, and the answer
   names no unit of the other namespace. A resource that a unit of each of
   the two namespaces holds is managed twice in both. Each finding names
   only the unit of its own namespace, and says that a unit of a linked
   namespace also holds the key. A link is installation data: its writes append
   an audit entry with the two namespaces and the person. The suite holds a
   case for each of the three reasons above.

   - **Unlink.** After an unlink, `Index.Holders` and `Index.Contested` of
     each namespace leave out the units of the other at once. The findings
     change at the next sweep that observes their keys. The next state check
     of each namespace resolves a finding of managed twice that the link
     made. A finding of not in any unit that the link resolved stays
     resolved until the next cloud sweep of its scope, which can be up to
     30 days later (`unmanaged.MaxEvery`): only a cloud sweep sees a
     resource that no unit holds. The suite holds the case of the state
     check.
   - **Keyed by ID.** A link holds the stable IDs of its two namespaces,
     never their names. A namespace that a person creates later under the
     name of a purged namespace has a new ID and gets no link of the purged
     one.
   - **Soft delete and restore of a namespace.** The soft delete of a
     namespace marks it deleted for its links in the same transaction
     (`Links.SetNamespaceDeleted`). Its links stop to classify at once:
     `Links.Linked` leaves them out, and `Index.Holders` and
     `Index.Contested` of the other namespace answer no unit of the deleted
     one. The links stay stored only so that a restore of the namespace in
     its recovery window brings them back.
   - **Purge of a namespace.** The purge of a namespace deletes its links
     and appends an audit entry, in the same transaction
     (`Links.DropNamespace`).

   The suite holds a case for each of these: a purge then a new namespace
   under the same name shows no link in `Linked` and in `Holders`, a soft
   delete stops the classification, and a restore brings the link back.

### The state check

10. **Left behind and managed twice need no sweep.** A **state check** reads
    the index of one namespace and classifies two sets of keys:
    - the **contested** keys: the keys that a unit of the namespace holds and
      that another distinct live unit of the namespace or of a linked
      namespace also holds, or that only deleted units hold
      (`Index.Contested`),
    - the key of each finding of the namespace that is not resolved, so a key
      that one live unit now holds resolves.

    It writes findings with the evidence `state`. A state check runs in the
    server, as a durable operation (ADR 72). It needs no runner and no
    cloud credential.
11. **When a state check runs.** One time each day for each namespace with at
    least one unit, from the schedule row of ADR 164 decision 16. Also after
    a soft delete, a restore or a purge of a unit, and on request with
    `namespace:start-sweep`. A state check is a sweep of the kind `state` in
    the records of ADR 164, so the console shows it with the cloud sweeps.
    - **Spread daily starts.** The daily state check of a namespace
      starts at a fixed time of the day that a hash of the namespace name
      gives, in steps of the 15-minute tick (`unmanaged.StateCheckOffset`).
      So the checks of an installation with 1,000 namespaces spread over
      the 96 ticks of the day, about 10 at each tick, and do not all start
      at midnight. A test holds the spread.
    - **A time target for each page.** The check reads the contested keys in
      pages of 1,000 (`Index.Contested`). One page, the read and the write of
      its findings, aims at 2 seconds (`unmanaged.StateCheckPageTarget`). A
      page above it logs a warning with the namespace and the time, so an
      operator sees a slow index. A namespace with 100,000 contested keys
      has 100 pages, about 3 minutes.
12. **A finding left behind after a purge.** A purge deletes the entries of
    the unit, and then no unit holds the key. A state check does not resolve
    the finding for that reason: only a sweep can tell that the resource is
    gone. The finding keeps the unit ID and the name of the unit at the time
    of the delete. A later sweep that sees the resource reclassifies it as
    not in any unit, and one that does not see it, in a complete scope,
    resolves it (ADR 164).

### Ports

13. **`unmanaged.Index`** holds the index: `ReplaceUnit`, `SetDeleted`,
    `DropUnit`, `Holders` and `Contested`. Each write carries the fence of
    the namespace of the unit. `Holders` and `Contested` take the namespace
    of the caller and answer its holders and those of its live linked
    namespaces only. **`unmanaged.Links`** holds the links: `Link`,
    `Unlink`, `Linked`, `SetNamespaceDeleted` and `DropNamespace`. The
    suite in `unmanagedtest` states the behavior of both, and
    a reference adapter in memory runs it today. The SQL adapters need an
    index on the normalized key. A lookup takes at most 1,000 keys.

## Consequences

- The server finds managed-twice resources across both tools, and across
  linked namespaces, with no cloud sweep and no cloud credential. A team gets the
  findings on the day after it adds a second unit that holds the same
  resource.
- The index adds one row for each managed resource of each unit, and one row
  for each key. An installation with 140 units of 300 resources each holds
  about 42,000 entries.
- The key rules cover AWS, Azure and Google Cloud resources that expose
  their cloud ID as an attribute. A resource with no such attribute has no
  key, and the index cannot find it managed twice. A sweep still sees it as
  not in any unit (ADR 164), and the person can ignore it with a reason.
- Two teams that share a cloud account and do not ask for a link each see
  the resources of the other team as not in any unit. The console says,
  on a finding of not in any unit, that a link can explain it.
- `GLOSSARY.md` gets the terms resource index, resource key, holder, sweep,
  state check, contested key, namespace link and finding.

## Requests to other designs

- **Glossary and retention (shared files).** "Retention sweep" becomes
  **retention expiry** in `GLOSSARY.md`, in the text of the blob purge, and
  in the draft history design (ADRs 150 to 153). The route
  `POST /api/server/deleted-units/sweep` becomes
  `POST /api/server/deleted-units/expire`. Then "sweep" names one concept.
- **Identity.** The action `server:link-namespaces`, held only by the roles
  that administer the installation.
- **Namespaces (shared files).** A namespace gets a stable ID that a new
  namespace under the same name does not reuse. The design of the soft
  delete, the restore and the purge of a namespace calls
  `Links.SetNamespaceDeleted` and `Links.DropNamespace` in their
  transactions (decision 9).

## Open questions

1. **Key rules of other providers.** Kubernetes objects, Cloudflare,
   Datadog and others. Should a namespace give its own rules, for example a
   type and an attribute, through the API?
2. **Resources with two keys.** A resource can have a second ID form, for
   example an AWS resource whose ARN changes with a rename. Is one key per
   resource enough for the first release?
3. **The visibility of "elsewhere".** With a link, a person learns that a
   unit of the linked namespace holds a key that they already see in their
   cloud. The administrator who made the link accepted this. Should a link
   be one-way, so a platform namespace sees the units of a team and not the
   other way?
