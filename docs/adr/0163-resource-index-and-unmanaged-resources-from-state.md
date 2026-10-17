# 163. The resource index and unmanaged resources found from state

Status: proposed · 2026-10-17

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
state of the units holds what they need. "Not in any unit" needs a scan of
the cloud (ADR 164). RFC 40 says that the managed-twice kind can come from
the inventory and from reading state.

The owner called the feature "sweep". `GLOSSARY.md` already has the
**retention sweep**: the task that purges deleted units. One word for two
things breaks the rule of one word for each concept. This ADR uses
**scan** for the work that finds unmanaged resources, and keeps "sweep"
for the retention sweep.

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
   index keeps the entries of the item with the highest order. The soft
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
   resource key in one namespace, from all its **holders** (the units of
   every namespace whose latest state holds the key) and whether a scan saw
   it. In order:
   1. Two or more distinct live units hold the key, and one of them is in the
      namespace: **managed twice**. Two resources of one unit are one holder.
   2. Exactly one live unit of the namespace holds the key: **managed**.
   3. Live units hold the key, none in the namespace: **elsewhere**.
   4. Only deleted units of the namespace hold the key: **left behind**.
   5. Only deleted units of other namespaces hold the key: **elsewhere**.
   6. No unit holds the key, and a scan saw it: **not in any unit**.
   7. Else: **none**.

   Managed, elsewhere and none make no finding. A table-driven test holds
   each case.
9. **Across namespaces.** Two teams can use one cloud account from two
   namespaces. So the classification reads the holders of every namespace
   of the installation. A resource that a unit of another namespace manages
   is **elsewhere**: it is not a finding of this namespace, and the answer
   names no unit of the other namespace. A resource that a unit of each of
   two namespaces holds is managed twice in both. Each finding names only
   the unit of its own namespace, and says that a unit of another namespace
   also holds the key.

### The state check

10. **Left behind and managed twice need no scan.** A **state check** reads
    the index of one namespace and classifies two sets of keys:
    - the **contested** keys: the keys that a unit of the namespace holds and
      that another distinct live unit also holds, or that only deleted units
      hold (`Index.Contested`),
    - the key of each finding of the namespace that is not resolved, so a key
      that one live unit now holds resolves.

    It writes findings with the evidence `state`. A state check runs in the
    server, as a durable operation (ADR 72). It needs no runner and no
    cloud credential.
11. **When a state check runs.** One time each day for each namespace with at
    least one unit, from a schedule row of ADR 72. Also after a soft delete,
    a restore or a purge of a unit, and on request with
    `namespace:start-scan`. A state check is a scan of the kind `state` in
    the records of ADR 164, so the console shows it with the cloud scans.
12. **A finding left behind after a purge.** A purge deletes the entries of
    the unit, and then no unit holds the key. A state check does not resolve
    the finding for that reason: only a scan can tell that the resource is
    gone. The finding keeps the unit ID and the name of the unit at the time
    of the delete. A later scan that sees the resource reclassifies it as
    not in any unit, and one that does not see it, in a complete scope,
    resolves it (ADR 164).

### Ports

13. **`unmanaged.Index`** holds the index: `ReplaceUnit`, `SetDeleted`,
    `DropUnit`, `Holders` and `Contested`. Each write carries the fence of
    the namespace of the unit. The suite in `unmanagedtest` states its
    behavior, and a reference adapter in memory runs it today. The SQL
    adapters need an index on the normalized key. A lookup takes at most
    1,000 keys.

## Consequences

- The server finds managed-twice resources across both tools and across
  namespaces with no scan and no cloud credential. A team gets the
  findings on the day after it adds a second unit that holds the same
  resource.
- The index adds one row for each managed resource of each unit, and one row
  for each key. An installation with 140 units of 300 resources each holds
  about 42,000 entries.
- The key rules cover AWS, Azure and Google Cloud resources that expose
  their cloud ID as an attribute. A resource with no such attribute has no
  key, and the index cannot find it managed twice. A scan still sees it as
  not in any unit (ADR 164), and the person can ignore it with a reason.
- `GLOSSARY.md` gets the terms resource index, resource key, holder, scan,
  state check, contested key and finding.

## Open questions

1. **Key rules of other providers.** Kubernetes objects, Cloudflare,
   Datadog and others. Should a namespace give its own rules, for example a
   type and an attribute, through the API?
2. **Resources with two keys.** A resource can have a second ID form, for
   example an AWS resource whose ARN changes with a rename. Is one key per
   resource enough for the first release?
3. **The visibility of "elsewhere".** A person learns that a unit of some
   other namespace holds a key that they already see in their cloud. This
   ADR thinks that this is acceptable. A security review decides.
