# Audit log

This guide shows how to read the audit entries of a namespace, check their chain and send a copy of each entry to a sink.
This guide does not describe retention settings or the full catalog of events.

## What the log records

Each response that gives a secret value in plain text writes one audit entry.
The reveal call writes one entry with the action `unit:read-secrets`, the
kind `reveal`, the state version in `version` and the value path, before it
answers.
The two Pulumi routes `decrypt/log-decryption` and
`decrypt/log-batch-decryption` write one entry each with
the action `unit:decrypt` and the kind `reveal`.
The Pulumi CLI calls them when a person asks it to show secrets, for example
`pulumi config --show-secrets`.

The routes that answer a secret value to a tool write one entry each with the
kind `tool-operation`, so a reader never confuses a tool that needed secrets
with a person who asked to see one. The state download is the exception that
the principal decides. A principal is a person, a service or a workload whose
identity OpenDustpan checked. The next list says how:

- The Pulumi `decrypt` and `batch-decrypt` routes write one entry each with
  the action `unit:decrypt`, the count of values in `valueCount` and an empty
  `version` and value path.
- The OpenTofu state download writes one entry with the action
  `unit:read-secrets`, the state version in `version` and an empty value
  path. The TFE-compatible API (`GET /api/v2/state-versions/{id}/download`)
  and the http backend (`GET /tofu/state/{namespace}/{workspace}`) both write
  it. The kind is `reveal` when a person acts, and `tool-operation` in all
  other cases. A person acts through a console session or an access token of
  a principal of the kind `person`. The bootstrap admin token has no
  principal record, so its kind is unknown, and the server counts an unknown
  kind as a person. A service token, a token from a trust rule and a job token
  write `tool-operation`. The `Dustpan-Channel` header never decides the kind.
  A principal with a kind that the server does not know cannot write an
  entry, so the download is refused and answers 500 with no content. Both
  routes need `unit:read` and `unit:read-secrets`, and answer
  `Cache-Control: no-store`.
- The OpenTofu route for one output writes one entry with the action
  `unit:read-secrets`, the state version in `version` and the value path
  `/outputs/<name>` when the output is sensitive. A non-sensitive output
  writes no entry.

The server sees the kind of the principal, but not who runs a CLI command.
A person who runs `tofu state pull` with a personal token makes a `reveal`
entry. A pipeline that runs `tofu plan` with a service token makes a
`tool-operation` entry. A person who uses a service token also makes a
`tool-operation` entry, and the audit entry of the creation of that token
names the person. Each `tofu plan` with a personal token writes one `reveal`
entry for each state download, and a `terraform_remote_state` read of
another workspace writes one more, so one plan can write more than one. To
keep CI reads as `tool-operation`, run CI with a service token or a workload
identity.
A failed append on a route that answers a secret value answers 500 with no
secret value, so a value without its entry never reaches the client.
The two `log-decryption` routes answer no secret value, so they keep
answering 204 when the append fails.

A restore or a purge of a deleted unit writes one entry with the
kind `change`, the action `unit:restore` or `unit:purge`, and the
stable ID of the unit in `objectID`. The store appends it in the same
transaction as the change, so a failed append changes nothing.
A purge of the scheduled retention expiry has the subject `@server`. The subject is the ID of the principal that
acted: a person, a service or a workload. A purge of an expiry run that a
person starts through the API has that person as the subject.

Each request for an access-review export writes one
entry with the kind `read-access`, the read-access action of the object and
the object `access-review <object> <mode>`. The mode is `snapshot
asOf=<time>`, or `history <from>-<to>`, in Unix milliseconds, followed by
the subject filter and `expand=members` when the request has them. The export of a namespace, a collection or
a unit writes its entry in that namespace, and the export of the
installation in `@server`.
The export of a collection holds the grants on the collection, the grants on
each unit in it now (a deleted unit that is not purged has the state
`deleted`), and the grants on the namespace and the installation that give
access to it. It holds no grant on another collection or on a unit of another
collection. A collection that does not exist answers 404. A failed append answers 500 with no rows.

An entry holds these fields:

- Who: `subject`, `idpSubject` (empty until an identity provider exists),
  `tokenID` (empty until tokens have IDs).
- What: `action`, `object` (a unit reference or a namespace), `version` and
  `valuePath` (for a state read, else empty), `valueCount` (the count of
  secret values of a decrypt call, else omitted).
- When: `atMs` (UTC, Unix milliseconds).
- Where: `sourceIP` (the remote address the server sees), `userAgent`
  (capped at 512 bytes), `channel` (`api` or `console`, as the client
  declares it in the `Dustpan-Channel` header). Unknown values are `api`.
  The server writes two more channels on its own: `startup` and
  `config-file` (see "Settings entries").
- `changes`: the keys of the entry with their values, else omitted. Each
  change holds `key`, `new`, and when they apply, `old`, `layer` and
  `takesEffect`. A `startup` or `config-file` entry holds its settings
  here.
- `reason`: the text that a person gave for the action, else omitted. It has
  at most 500 Unicode code points. A reason that is too long, or that holds
  a control character, a format character or a line or paragraph
  separator, is refused. A force-unlock can carry one.
- `principalKind`: the kind of the principal record that acted: `person`,
  `service` or `workload`. It is omitted when the server cannot name the
  kind, for example for the bootstrap admin token.
- `jobID`: the job whose token acted, else omitted.
- Kind: `tool-operation`, `reveal`, `override`, `change`, `read-access`,
  `audit-read` or `security`, never anything else, so a reader never confuses a tool that needed secrets
  with a person who asked to see one.
- Outcome: `success`, `failure` or `denied`.
- `requestID`: the request ID the server mints. The server returns it in
  the `X-Request-Id` response header and ignores an ID the client sends.
- `objectID`: the stable ID of the unit that the entry names. The
  store sets it for each entry on a unit that exists, and for the delete,
  restore and purge of a unit. The list omits it when the entry has none.
- `prevHash` and `hash`: SHA-256 over a fixed, documented encoding of the
  fields above and `prevHash` (see `ComputeHash` in `pkg/audit`).
  The encoding writes each field as its length in bytes (decimal), a
  colon and the bytes of the field, with nothing between the fields. For
  example, the subject `admin` is `5:admin` and an empty field is `0:`.
  After `prevHash`, the encoding always writes seven more fields, in this
  fixed order: the decimal value count, the stable ID of the unit, the
  canonical JSON of the changes (sorted by key) as the store holds it, the
  decimal failover version of the namespace (0 in `@server`), the principal
  kind, the job ID and the reason. The reason stays last. No release shipped
  an audit entry, so the order can change, and an entry from an earlier build
  does not keep its hash. No field is optional. An entry with no value count
  writes one `-` in place of the count, so an entry with no count (`-`) and an entry with the count 0
  (`1:0`) have different hashes. Because each field carries its length,
  two different entries never give the same encoding.
  The first entry of a namespace has an empty `prevHash`.

## Force-unlock entries

Each force-unlock of an OpenTofu workspace writes one entry of the kind
`change` with the action `unit:write-forced`. The TFE-compatible API and the
http backend both write it. The server writes it in the transaction that
releases the lock, so a release without its entry does not happen.

- `subject`: the principal that forced the release.
- `version`: the serial of the new state version that the release wrote.
  It is empty when the release wrote no version: a workspace with no state,
  or a state that the client encrypted.
- `changes`: the released `lock-holder` and the time `locked-at` of the lock,
  each in `old`.
- `reason`: the reason that the caller gave, else omitted. Only the
  TFE-compatible API can carry a reason. The lock info of the http backend
  has no field for one.

A force-unlock of a state that the client encrypted also writes an entry of
the kind `override`, as a forced write does.

Maintenance mode also writes this entry. When it starts, it releases each
lock that it finds. Each release writes one `change` entry with the action
`unit:write-forced`, the subject `maintenance` and the reason
`maintenance mode started`.

Reads of the audit log itself are not logged yet.

## Security entries

The server writes one entry of the kind `security` and the outcome `denied`
when it stops an access that crosses a namespace boundary. Today this is a
read, a write, a list or a delete of one of these blobs, with a blob key of
another namespace, or with a key of its own namespace that is outside the
directory of these blobs:

- An item key blob, in the `item-keys` directory of the namespace.
- The content of an OpenTofu state version, in the `tofu` directory of the
  namespace.
- A Pulumi checkpoint blob or a journal batch blob, in the `pulumi` directory
  of the namespace.

The action is `blob:out-of-scope`. The entry is in the installation log
(`@server`), the subject is `server`, and the object is the ID of the namespace of the call. The reason
names the blob, the operation, and whether the key was outside the namespace,
outside the directory of the blob in the namespace, or outside the directory of
the unit in the blob directory, for example
`state blob get refused: key outside the namespace`. The entry never holds the
blob key that the call used.

For a Pulumi blob, the directory of the unit is the directory of one stack:
`namespaces/<namespace ID>/pulumi/<stack ID>/`. A key in the directory of
another stack of the same namespace is a refusal too. The server checks this
each time that it uses a key from a row, to read, to delete or to list.

The channel of the entry is the channel of the request: `console` for a
request from the console, and `api` for all other requests. Only the Pulumi
routes set the `console` channel for these entries today. A refusal that a
request on an OpenTofu route causes has the channel `api`, also when the
request comes from the console.

A request for a Pulumi blob that meets such a refusal fails with a server
error, and the entry is the record for the operator. The server stores the
checkpoint and the journal batches of a stack under
`namespaces/<namespace ID>/pulumi/<stack ID>/`, so a refusal shows a damaged
row or a fault in the server, not a mistake of the client.

## Denial entries

Each request that the server refuses because the principal does not hold the action writes an entry with the outcome `denied`.
Repeated refusals in a window of 1 minute write one entry and a count (see [The limit on repeated denials](#the-limit-on-repeated-denials)).
Every API writes it. These are the Pulumi Cloud-compatible API, the TFE-compatible API, the OpenTofu HTTP backend, the inventory, context, audit, profile and runner APIs, and the installation API.
A list that shows only the items that the principal may read does not write an entry for each item that it leaves out.
The entry is in the namespace of the object. An object with no namespace, or with a namespace that does not exist, writes the entry in the installation log (`@server`). For a namespace that does not exist, the `reason` ends with `(namespace NAME does not exist)`.
A request with no valid credential answers 401 and writes no entry, because it has no principal.

- `subject`, `tokenID`, `principalKind` and `jobID`: the principal that was refused.
- `action` and `object`: the action that the route needs, and the object that it needs it on.
- `reason`: `denied by policy on` and the route, for example `denied by policy on GET /api/audit/entries`. The route is the matched pattern, or the method and the path. It never holds the query.
- `kind`: `reveal` for a refused read of secret values, `override` for a refused forced write, `tool-operation` for a refused read, else `change`.
- `channel`: `console` for a request that the console session signed in, whatever its `Dustpan-Channel` header says. For a request with an access token, the channel that the header declares, and `api` for an unknown value or no header.

The check of the action can fail with an error that is not a refusal. The request then gets that error, and the server writes an entry too.
The `reason` is then `authorization failed on` and the route. It never holds the text of the error.

An entry holds no request body, no header value and no secret.
In the subject, the object and the reason, the server writes each control character, format character, line separator and paragraph separator as `\uXXXX`.
It replaces each byte that is not valid UTF-8 with U+FFFD.
It cuts the subject to 255 bytes and the object to 512 bytes.

A refusal that a domain package decides also writes an entry, in the place where it happens.
These refusals write an entry:

- An OpenTofu `UNLOCK` that names a lock that is not the current lock, and an `UNLOCK` that finds the lock changed or held by another principal. The `action` is `unit:lock`.
- The removal of a manual lock by a principal that is not the holder, and a removal that finds the lock changed. The `action` is the action of the route: `unit:lock` for the removal, `unit:manage` for the force-unlock.
- A grant, a role or a membership add that gives more than the principal who makes it holds. The `action` is the action of the route. A refusal that names the missing actions writes `outside the reach of the caller: ` and then each missing action with its object, for example `outside the reach of the caller: server:backup on the installation`. A refusal with no missing action keeps `grant gives more than the granter holds`.
- A create or a rotation of a token whose service principal reaches outside the reach of the caller. The `reason` names the missing actions and their objects, as above.
- A state write or a force-unlock that the workspace refuses because the forced-write or unlocked-write switch is off or not granted. A write without the lock to a workspace that no one has locked is such a refusal in two cases. The switch is off, or the principal lacks `unit:write-unlocked`. The client still gets its lock conflict. A write that is refused only because another principal holds the lock writes no entry here. The `action` is `unit:write-forced` or `unit:write-unlocked`, the `kind` is `override` and the `reason` is `override refused`.

The `reason` says which refusal it was.
A manual lock that is already held answers 409. A manual lock that does not exist answers 404. Neither writes an entry, because they report the state of the object.

If the server cannot write the entry, the request still gets its refusal. The server logs the failure and counts it in `dustpan_audit_append_failures_total` (see "Alert on failed logging").
The server writes the entry also when the client closes the connection. A write that takes longer than 5 seconds counts as a failure.

### The limit on repeated denials

A principal that sends many refused requests must not fill the log.
All APIs of the server share one limit.
For each principal, action, API, kind of object and namespace, the server writes the first denial in a window of 1 minute.
It counts each other denial of that key in the window and writes nothing for it.
The count goes out in the `reason` of the first entry of the next window (`N further refusals since the last entry`).
When the principal stops, the first denial of any principal after the window writes one entry with the action and the count (`N further refusals in the last window were not recorded one by one`).
The kind of a stack or a workspace includes its tool (`unit:pulumi`, `unit:opentofu`).
A refusal on a workspace does not hide a refusal on a stack in the same namespace.
The API is part of the key too.
A refusal on the TFE-compatible API and a refusal on the OpenTofu HTTP backend write one entry each.
Each namespace that does not exist counts as the same namespace, so a principal cannot make keys with names that it invents.

The server tracks at most 4096 keys, and at most 64 keys for each principal.
A new key of a principal that has 64 keys goes to the aggregate key of that principal.
The entry of this aggregate names the principal, and its flush entry has the action `*` and is in the installation log (`@server`).
A denial of a new key beyond the 4096 keys adds to one aggregate for all principals.
The aggregate writes one entry for each window in the installation log (`@server`). The entry has the action `denials:suppressed` and the subject `denial-limiter`.

The server starts no background task for the limit, so the count of a quiet key waits for the next denial.
When the server stops with a signal, it writes the counts that it still holds, within 5 seconds.
A server that ends in another way loses these counts, and a restart starts with no counts.

### Refused dev sign-ins

A production installation refuses a session or a token of a dev person. A dev person has a subject that starts with `dev:`, in any case.
Only `opendustpan dev` makes such a person. One on a production installation comes from a restored dev backup.
Each refusal can write an entry with the outcome `denied`, in the installation log (`@server`):

- `subject`: the subject of the dev person.
- `action`: `session:sign-in`.
- `object`: `@server`.
- `reason`: `sign-in by a dev person refused on a production installation`. When the server counted refusals before this entry, the `reason` ends with `; N further refusals since the last entry`.

The entry holds no token, no session cookie and no request data.

These entries use the limit of the previous section, with a window of 1 hour.
The server writes at most 1 entry for each dev person for each hour.
It counts each other refusal of that person in the hour.
The count goes out in the first entry of the next hour.
When the table of the limiter is full, a dev refusal uses the overflow slot.
The overflow slot has a window of 1 minute.
Then the limit of 1 entry for each person for each hour does not hold.
When the refusals of the person stop, the first denial of any principal after the hour writes an entry.
The `object` of this entry is the key `signin/server`.
The entry holds the count (`N further refusals in the last window were not recorded one by one`).
When the server stops with a signal, it writes the counts that it still holds.
A dev installation, and a person whose subject does not start with `dev:`, write no such entry.

## Settings entries

Each server process writes entries about its own settings in the installation
log (`@server`). The subject is the ID of the server process: a new `ins-` ID at
each start, which the start log shows on the line `instance`.
The action is `server:manage-settings`, and the kind is `change`.

- At each start, the server writes one entry with the channel `startup`.
  Its `changes` hold each setting with its `key`, its value in `new` and
  its `layer` (`flag`, `env`, `file` or `default`). A change of the file or of a
  variable while the server is off shows as the difference between two
  start entries.
- When a reload of the configuration file changes a value, the server
  writes one entry with the channel `config-file`. Its `changes` hold each
  changed setting with its `old` and its `new` value, and `takesEffect`.
  No setting applies while the server runs yet, so `takesEffect` is
  `next start`: the entry records a change, and the new value applies at
  the next start.

- A change through the [settings API](../reference/settings-api.md) writes one
  entry with the channel `api`. The subject is the principal that made the
  change. The action is `server:manage-settings` for the installation. For a
  namespace, the entry is in the log of the namespace and the action is
  `namespace:manage-settings`. One request writes one entry, with one change
  for each key. The `layer` of a change is the layer of the new value, for
  example `database`. A request that the server refuses because of its
  content writes one entry with the outcome `failure`.

The API reads the installation log (`@server`) with
`organization=@server`. The [Read the entries](#read-the-entries) section
gives the action that the read needs.

A sensitive setting (the database URL and each secret) shows only `set` or
`not set`, never the value. When the server cannot write the start entry,
the start stops. When it cannot write the entry of a reload, it keeps the
old configuration and the reload fails.

## Read the entries

`GET /api/audit/entries?organization=<namespace>` lists one page of the
entries of the namespace, newest first, with the total count.
It needs the action `namespace:read-audit` on the namespace.
Send the access token as `Authorization: token <token>` or `Authorization: Bearer <token>`.
`page[number]` selects the page (from 1) and `page[size]` selects its size
(up to 200). Both default when missing.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/entries?organization=acme&page[size]=50"
```

`GET /api/audit/entries/<sequence>?organization=<namespace>` reads one
entry of the namespace by its sequence, in the same shape as an entry of the
list. It needs the same action. A sequence that the namespace does not hold
answers 404.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/entries/7?organization=acme"
```

`GET /api/audit/check?organization=<namespace>` walks the chain of the
namespace oldest first and answers whether it is valid.
It needs the same action.
The check reads the chain in pages of 100 entries, so a large log never
grows the check without a bound.
A valid chain answers `{"valid":true,"entriesChecked":N,"entriesUnchained":U}`.
A broken chain answers `{"valid":false,"badSequence":S,"entriesChecked":N,"entriesUnchained":U}`,
where `S` is the first entry that does not match.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/check?organization=acme"
```

`GET /api/audit/export?organization=<namespace>` sends the entries of the
namespace oldest first, one JSON object for each line, or one CEF line
for each entry with `format=cef`. A CEF line does not hold every field that
the hash covers, so the offline hash check needs `format=json`. It needs the same
action. Use it to keep a copy of a range, or to check a range with no help
from the server. The lines hold the previous hash and the hash, so a reader
can compute each hash again. See
[Audit export API](../reference/audit-export-api.md) for the query values, the
fields and the steps of the offline check.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/export?organization=acme&fromSequence=100" \
  > acme-audit.jsonl
```

Each export writes one entry of the kind `audit-read` in the log that it
reads. If the server cannot write that entry, it sends no line.

`GET /api/audit/export?organization=<namespace>` sends the entries of the
namespace oldest first, one JSON object for each line. It needs the same
action. Use it to keep a copy of a range, or to check a range with no help
from the server. The lines hold the previous hash and the hash, so a reader
can compute each hash again. See
[Audit export API](../reference/audit-export-api.md) for the query values, the
fields and the steps of the offline check.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/export?organization=acme&fromSequence=100" \
  > acme-audit.jsonl
```

Each export writes one entry of the kind `audit-read` in the log that it
reads. If the server cannot write that entry, it sends no line.

The installation writes the entries of an action that is not in a namespace,
such as a namespace create, to the installation log (`@server`). Read that log with
`organization=@server`. It needs the action `server:read-audit` on the
installation instead, and the same paging. The read of one entry and the
check work the same way:

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/entries?organization=@server"
```

A new namespace audit log starts with the grant of its creator.

## Turn the chain on or off

The hash chain is on by default.
`--audit-chain` (`DUSTPAN_AUDIT_CHAIN`, or `audit-chain` in the settings file) takes `on` or `off`.
The server reads the setting once at start.
No API call and no reload of the settings file changes it while the server runs.

The database keeps the state of the chain: `on` or `off`, when it last changed, and who changed it.
An installation without that state counts as `on`.
At start, the server compares its setting with the stored state.
When they agree, the server starts normally.

When they differ, the server enters maintenance mode and changes nothing yet.
A person approves the change in the console.
The console calls `POST /api/server/audit/chain`.
The approval sets the chain to the setting of the server that receives it.

Sign in to the console before maintenance mode starts.
Maintenance mode starts when the server restarts with a different `--audit-chain`, and it refuses a new sign-in.
So sign in first and restart the server with the new setting.
Then approve the change before the sign-in is older than the token freshness window of the session policy (15 minutes by default).
See [Console sign-in](console-sign-in.md) for the window.

The approval needs a console session with a recent sign-in, in both directions.
An access token answers 403 with the error code `mfa.fresh-step-required`.
A session with an old sign-in answers 401 with the same error code.
The CLI cannot give a fresh authentication.
`opendustpan audit-chain approve` sends no request.
It prints where to approve and ends with an error.

`opendustpan audit-chain status --server https://dustpan.example` shows the state of the chain and whether a change waits.

- Turning the chain on needs the action `server:set-audit-chain` (operator family).
- Turning the chain off needs `server:set-audit-chain` (operator family) and `server:disable-audit-chain` (administration family).
  A service principal cannot approve it: a person must approve turning the chain off.

The approval writes an entry in `@server` with the kind `change`, the action `server:set-audit-chain` and the value path `/audit/chain/on` or `/audit/chain/off`.
Then the server migrates the log in the background.
It waits 4 seconds, so every server process refuses requests.
It writes one entry in each namespace and in the installation log that holds entries:

- **On:** a start entry with the action `server:start-audit-chain`.
  Its hash has an empty `prevHash`.
  Its changes hold `digest`, a running SHA-256 of every earlier entry of the same namespace or installation, and `entries`, their count.
  The earlier entries keep their rows.
- **Off:** an end entry with the action `server:end-audit-chain`, which names the person who approved the change.
  Its hash links to the entry before it, so the chain ends with a chained entry.
  Later entries hold an empty `prevHash` and an empty `hash`.

Then the server stores the new state, waits 4 seconds so every server process can see it, and leaves maintenance mode.
`opendustpan audit-chain status --server https://dustpan.example` shows `the migration runs` until then.
A migration that stops before it ends keeps the old state, so the change waits for an approval again.
A restart with the stored setting cancels a waiting change.
The installation then leaves maintenance mode.
Approve a waiting change in the console.

While a change waits or migrates, every server process answers 503 on most routes.
The probes, GET of the metrics route, GET and POST of the audit chain route, the maintenance routes and the console files stay available.
The server refuses a restore while a change waits, and refuses an approval while a restore runs.
A server process whose setting differs from the stored state also answers 503 on most routes, also after another server process approved the change.
The maintenance routes stay available, so an operator can clear stale active work on a server process whose setting differs.
Restart each server process with the stored setting.

The server computes the digest of a start entry over each earlier entry in order of sequence.
For each entry, the next digest is the hex SHA-256 of four parts joined with the unit separator (0x1F).
The parts are the previous digest, the entry hash, the stored `prevHash` and the stored `hash`.
The previous digest of the first entry is the empty string.
The entry hash is the hash of the stored fields of the entry with an empty `prevHash`.

The hash takes no key: any installation can compute it again from the stored fields.
Each entry of a namespace holds `fenceVersion`, the failover version of the namespace when the server appended it.
The hash covers it, so a verifier can see where a forced failover forked the chain of a namespace.
Entries in `@server` hold 0. The hash covers the 0, and the audit API omits the field for them.
A verifier who kept a digest outside the database can compute it again from the old rows.
So the verifier can prove that the old rows did not change after the chain started.

The check of a namespace or of the installation starts at its last start entry, or at sequence 1 when it holds none.
From there, each entry holds a hash that matches its fields and links to the entry before it, with no gap.
The chain goes up to now, or up to an end entry.
After an end entry, entries hold no hash.
A start entry also checks its digest against the earlier entries.
With the chain on, a namespace or the installation must not end after an end entry.
With the chain off, a namespace or the installation without a chain is valid
only when its first entry comes after the time the chain went off.
A row below sequence 1 fails the check.
The store keeps the times of each namespace and of the installation in order.
An entry whose time is earlier than the last entry of its namespace or of the installation takes the time of that entry.
The check answers `entriesUnchained`: the count of entries without a hash.
The check cannot prove that an entry before a start entry, or after an end entry, is unchanged.
Signed checkpoints (see below) and the digest cover that case.

A backup does not carry the state of the chain.
A restore keeps the state of the target installation.
After the swap, the server compares the restored log with that state.
With the chain on, each namespace and the installation must end with a chained entry that is not an end entry.
With the chain off, each namespace and the installation must end with an entry without a hash, or with an end entry.
When the log agrees, the server checks the chain of every namespace and of the installation.
When that check fails, the restore still succeeds.
The server then logs an error and adds one to `dustpan_audit_restore_check_failures_total`.
When the log does not agree, the installation stays in maintenance mode until a person approves the change.
The approval runs the same migration on the restored log.
A restore that stops after the swap gets the comparison at the next start.
A restore trusts the audit log in the backup: signed checkpoints (see below) protect a backup.

The server checks the chain of every namespace and of `@server` at start and every hour.
One server process runs each check, as the schedule `audit-chain-check`.
The server process stores the result in the database.
Each server process reads the stored result when Prometheus scrapes it, so all server processes export the same value.
`dustpan_audit_chain_invalid` holds the larger of two counts.
The first count is the namespaces and the installation that do not match in the stored result.
The second count is the namespaces and the installation with a stored mark in the current epoch.
A check that ends late cannot clear the alarm while a mark stands.
When a server process cannot read the stored result, it exports 1, adds one to `dustpan_audit_chain_read_errors_total` and logs a warning at most once each minute.
A database fault never shows as a good chain.
When `dustpan_audit_chain_read_errors_total` increases together with `dustpan_audit_chain_invalid`, look for a database fault before you look for tampering.
When a database read, a blob read or a checkpoint write fails, the server does not check that namespace or the installation.
It logs a warning and adds one to `dustpan_audit_chain_check_errors_total`.
Such a check does not lower `dustpan_audit_chain_invalid`.
A failed read during the check of a restored log also adds one to `dustpan_audit_chain_check_errors_total`.

```yaml
      - alert: DustpanAuditChainInvalid
        expr: dustpan_audit_chain_invalid > 0
        labels:
          severity: critical
        annotations:
          summary: An OpenDustpan audit chain does not match
      - alert: DustpanAuditChainCheckErrors
        expr: increase(dustpan_audit_chain_check_errors_total[3h]) >= 3
        labels:
          severity: warning
        annotations:
          summary: The OpenDustpan audit chain check cannot read the log
      - alert: DustpanAuditRestoreCheckFailed
        expr: increase(dustpan_audit_restore_check_failures_total[1h]) > 0
        labels:
          severity: critical
        annotations:
          summary: A restored OpenDustpan audit log does not match its chain
```

## Sign checkpoints

Signed checkpoints are opt-in.
They show a rewrite of the log by a person who can write to the database and compute the chain again.
`--audit-checkpoints` (`DUSTPAN_AUDIT_CHECKPOINTS=true`) turns them on.
They need `--audit-chain=on` and the signing key file that `--audit-signing-key-file` (`DUSTPAN_AUDIT_SIGNING_KEY_FILE`) names.
The server does not start when one of the two is missing.

The key file holds a 32-byte Ed25519 seed in base64.
The file must not be readable by group or others.
The server never makes the key and never derives it from the key-encryption key.
Make a key with this command:

```sh
(umask 077 && openssl rand -base64 32 > audit-signing.key)
```

At each check, the server does these steps for each namespace and for the installation whose chain matches:

1. It checks the checkpoints of the namespace or the installation in the current epoch.
   It reads every checkpoint that is newer than the last checkpoint that matched, up to 256.
   It also reads a random sample of 8 older checkpoints.
   It always reads the newest checkpoint of the namespace or the installation.
   When the log ends before that checkpoint, the check fails.
   The next check finds a cut at the end of the log.
   It reads every copy of each sequence that it checks.
   For each copy, it checks the key, the key ID, the signature and the epoch.
   The epoch in the checkpoint must match the epoch of its directory.
   Two copies of one sequence that sign different hashes make the check fail, before the server reads the database.
   Then it checks the hash of the entry at the sequence.
2. If all of them match, it lists the copies of the sequence of the last entry and checks each one.
   When it finds a copy, it signs nothing more.
   Otherwise, it signs the hash of the last entry of the namespace or the installation.
   The checkpoint holds the namespace (`@server` for the installation), the sequence, the epoch, the hash, the time, the key ID and the signature.
   The key ID is the first 16 hexadecimal characters of the SHA-256 hash of the public key.
3. It writes the checkpoint to the blob store as a new copy, under a key that holds a random attempt part.
   [The layout of the audit checkpoint keys](../reference/audit-checkpoint-layout.md) gives the keys.
   No write replaces a stored copy, also on a store that ignores conditional writes.
   Two server processes that sign the same sequence at the same time store two copies, and the check finds no mismatch.

A namespace or the installation with a checkpoint that does not match counts in `dustpan_audit_chain_invalid`, and the server logs its name and the sequence.
The server never signs for a namespace or the installation whose chain or checkpoints do not match.
Once a checkpoint does not match, later checks fail until a new epoch starts.
The server process that finds the mismatch stores a mark in the database.
Every server process reads the mark before it signs, so a mismatch that one server process finds stops signing for that namespace or the installation. A mark of one namespace or the installation does not stop the signing for another.
Other namespaces and the installation keep signing, and each gets its own check.
So `dustpan_audit_chain_invalid` stays the count of namespaces and the installation that do not match.
The mark stays across a restart of any server process, and only a new epoch ends it.
When a server process cannot read the mark, it signs nothing, counts that namespace or the installation as invalid and logs the error.
When a server process cannot store a mark, it keeps the mark in memory and signs nothing for that namespace or the installation.
It stores the mark at its next check.
When a stored copy holds another hash, the write fails and that namespace or the installation stays invalid.
The stored copy stays, so a later check also finds the mismatch.
The database never decides which copy counts.
An object that the server did not write can be under the directory of a namespace or the installation.
Such an object also stops signing for it, before the server checks a signature.
Examples are a key with another name, or a key with a valid name and a sequence above the end of the log.
This is on purpose: the server signs nothing while it cannot explain an object, and the object stays as evidence.
The server lists the stored checkpoints of a namespace or the installation first, and then reads the last entry of the namespace or the installation.
The log only grows.
So a stored checkpoint above the last entry shows a cut log.
It never shows a checkpoint that another server process stored a moment ago.
While a change of the chain waits or migrates, the server checks the checkpoints but signs none.

The server logs the key ID and the public key in base64 at start.
Keep a copy of the public key outside the server.

Keep the blob store apart from the database: give the two stores different credentials.
If the blob store has object lock, turn it on for the prefix `audit-checkpoints/`.
An entry is safe from a rewrite only after a checkpoint signs it, so the window is at most one hour.
A person who can delete checkpoints can hide a rewrite of the entries that came before the next checkpoint.
Each server process remembers the newest checkpoint that matched, so a later check finds deleted checkpoints.
A server process forgets this at a restart, so without object lock only later checks find a deletion from before a restart.

### Epochs

An epoch is one history of the log.
The check reads only the checkpoints of the current epoch.
The server keeps the epochs in the blob store under `audit-checkpoints/epochs/`.
Each start of an epoch writes a new copy of the epoch object.
The current epoch is the highest epoch whose copies all have a valid signature.
Each epoch object holds the epoch number, the time, the key ID and a signature.
The server signs each epoch object with the same key as the checkpoints.
The signed message holds a fixed context line, the epoch, the time in Unix milliseconds and the key ID.
The check reads every copy of every epoch object and verifies each one.
It checks the signature, the key ID, and the body number against the key number.
An unsigned or badly signed epoch object makes the check fail and stops new signatures.
A bad epoch object counts that namespace or the installation in `dustpan_audit_chain_invalid`.
After an investigation, the operator deletes the bad epoch object.
The next check then reads the fixed store.

A restore starts a new epoch, because a restore of an older backup deletes entries that older checkpoints sign.
The restore writes a change entry in the installation log (`@server`) with the value path `/audit/checkpoints/epoch/<number>`.
The checkpoints of older epochs stay in the blob store.
The server does not check them again.
No sweep of the server deletes a key under `audit-checkpoints/`: each copy is evidence.

### Change the signing key

A checkpoint or an epoch object that a key signed passes the check only while the server knows that key.
Before you change the signing key, put the old public key in a file and name the file with `--audit-verify-keys-file` (`DUSTPAN_AUDIT_VERIFY_KEYS_FILE`).
The file holds one public key in base64 on each line.
The server ignores empty lines and lines that start with `#`.
The server uses these keys only to check checkpoints and never signs with them.
The server does not start when the file holds a line that is not a 32-byte Ed25519 public key in base64.

### Clear the alarm

A checkpoint that does not match keeps `dustpan_audit_chain_invalid` above zero, and the stored mark keeps every server process from signing for that namespace or the installation.
First find the cause: compare the entries of that namespace or the installation with your audit log sinks and with the backups.
When you know the cause, start a new epoch.
The server then checks only the checkpoints that it signs after this time.

```sh
curl -X POST -H "Authorization: Bearer $DUSTPAN_ADMIN_TOKEN" \
  https://dustpan.example/api/server/audit/checkpoint-epochs
```

The request needs the action `server:manage-audit`.
It answers 201 with the number of the new epoch, and it writes a change entry in the installation log (`@server`).
It answers 409 when signed checkpoints are off.
Each server process reads the new epoch at its next check.
A mark from an older epoch stops nothing in the new epoch.

## What the chain protects against

The chain shows a change or a deletion of an entry.
Each entry holds the hash of its own fields and of the previous entry of
its namespace, so a changed field breaks the hash of that entry and a
deleted entry breaks the link of the next one.
A sink copy is a whole entry: the sink never cuts a field and never sends a changed entry.
Each copy keeps `scope`, `sequence`, `prevHash` and `hash`, so a receiver can follow the links and find gaps and duplicates.
The full entry stays in the server audit log, and the chain check on the server covers it.
A backup carries the `audit_entries` table, so a restore keeps the entries
and a valid chain.

The chain does not stop a person who can write to the database and compute
the chain again.
Such a person can rewrite the entries and their hashes, and the check still
answers valid.
Signed checkpoints in the blob store are the anchor outside the database (see above).
Protect the database and its backups as the guide for backups says.

## Alert on failed logging

A failed append writes a log line with no secret and no value and counts
`dustpan_audit_append_failures_total`.
On the two `log-decryption` routes the call still answers, so the counter is
the only record of the lost entry besides the log line.
On a route that answers a secret value the call answers 500 with no value.
Alert when the counter is above zero.
We designed this alert against NIST SP 800-53 AU-5 and PCI DSS v4.0.1
requirement 10.7, which ask for an alert when audit logging fails. This is not
a compliance claim.

```yaml
groups:
  - name: dustpan
    rules:
      - alert: DustpanAuditAppendFailures
        expr: dustpan_audit_append_failures_total > 0
        labels:
          severity: critical
        annotations:
          summary: OpenDustpan lost audit entries
```

## Send a copy to a sink

The server can send a copy of each audit entry to one or more sinks.
The SQL table stays the record of the audit log.
No sink is the default.

Give `--audit-sink` once for each sink.
In `DUSTPAN_AUDIT_SINK` and in the key `audit-sink` of the configuration file, separate the sinks with spaces or line breaks.
The start log shows only whether `audit-sink` is set, because a webhook URL can hold a token.
A sink item can hold a comma, for example in the query of a webhook URL.
Each item is `[<name>=]<sink>`:

| Sink | Example | What it writes |
| --- | --- | --- |
| A file | `file:/var/log/dustpan/audit.jsonl` | One JSON line for each entry. The server syncs the file after each batch. |
| Syslog | `syslog+tls://logs.example:6514` | One RFC 5424 message for each entry, over `udp`, `tcp` or `tls`. |
| A webhook | `webhook:https://siem.example/hooks/dustpan` | One HTTPS POST for each batch, with a JSON array of entries. |
| OTLP logs | `otlp:https://collector.example:4318` | One OTLP/HTTP POST for each batch, with one log record for each entry. |
| Object storage | `blob:audit` | One gzip file of JSON lines for each batch, in the blob store of the server. |

```sh
opendustpan serve \
  --audit-sink 'siem=syslog+tls://logs.example:6514' \
  --audit-sink 'archive=blob:audit'
```

The name keys the cursor of the sink in the database.
Without a name, the kind is the name: `file`, `syslog`, `webhook`, `otlp` or `blob`.
Two sinks of one kind need names.
Keep the name when you change the address of a sink.
A new name starts from the first entry and sends every entry again.

### What a sink receives

Each entry is one JSON object with `time`, `level` (`info`), `msg` (`audit entry`) and the fields of the audit API.
`scope` names the namespace, or `@server` for the installation.
`sequence` numbers the entries of one namespace or of the installation.
`valueCount`, `objectID` and `changes` are present only when the entry has them.
`changes` holds the canonical JSON that the hash covers: a list sorted by `key`.
`prevHash` and `hash` hold the chain of the entry.
The server never cuts a field: each copy is a whole entry with `scope`, `sequence`, `prevHash` and `hash`.
A receiver can check the link to the record before it, and can find gaps and duplicates by `scope` and `sequence`.
The full entry stays in the server audit log, and the chain check on the server covers it.

```json
{"time":"2026-10-13T12:00:00.000Z","level":"info","msg":"audit entry","scope":"acme","sequence":7,"atMs":1791892800000,"subject":"admin","action":"unit:read-secrets","object":"opentofu:acme/prod","kind":"reveal","outcome":"success","prevHash":"9f2c...","hash":"41ab..."}
```

The server reads the new entries of each namespace and of the installation in order and writes them to the sink in one batch.
It moves the cursor of the sink only after the sink accepted the batch.
A failed batch stays, and the server sends it again with a longer wait each time, up to 5 minutes.
While its lease holds, one server process ships a sink. After the server loses the lease or a round fails, a sink can receive an entry twice. Delivery is at least once.
Drop duplicates by `scope` and `sequence`.
A batch for one namespace or the installation that fails does not stop the others.
The server sends a batch every 5 seconds to a file, syslog, a webhook or an OTLP receiver, and every minute to object storage.

### Syslog

Each message has the facility `log audit` (13) and the severity `info`.
Set another facility with `?facility=<0 to 23>`, for example `syslog+udp://logs.example:514?facility=16`.
The message part is the JSON object above.
STRUCTURED-DATA and MSGID are `-`.
Over `tcp` and `tls` each message starts with its length in bytes (octet-counting framing: RFC 6587 section 3.4.1 over TCP, RFC 5425 section 4.3 over TLS).
UDP has no acknowledgment, so a message can get lost without an error.
Use `tls` for an audit log.
`?ca-file=<path>` names a PEM bundle to trust instead of the system store.
`?max-size=<bytes>` sets the largest message: from 480 to 65507 bytes over UDP, and from 480 to 65536 bytes over TCP and TLS.
The default is 8192 over UDP and 65536 (64 KiB) over TCP and TLS, which holds the largest entry the server writes.
Raise the matching limit of the receiver to at least the same size, or the receiver drops the longer messages.
With rsyslog set `$MaxMessageSize`. On a TCP input also set `MaxFrameSize`.
The defaults of both may be below 64 KiB. Check the receiver's limit.
With syslog-ng set `log-msg-size()`. Its default may be below 64 KiB. Check the receiver's limit.
The sink never shortens a field and never sends a changed entry.
The sink refuses an entry above that size and never sends it.
The server counts it in `dustpan_audit_sink_skipped_total` and in
`dustpan_audit_sink_failures_total{reason="size"}`. It logs one error line with the
`scope` and the `sequence` of the entry, and goes on with the next entry.
Read the refused entry by its `scope` and `sequence`, for example `GET /api/audit/entries/7?organization=acme`.
The full entry stays in the server audit log, and the chain check on the server covers it.

### Webhook

The URL must use HTTPS.
The server sends only to a public address and does not follow a redirect.
The start fails when the URL names a blocked address, for example a loopback or metadata address.
A connection to a blocked resolved address fails, and the sink status shows the failure.
For a receiver on a private network, set `--audit-sink-allowed-ranges`.
Set the signing key in `DUSTPAN_AUDIT_WEBHOOK_SECRET`, or in a file that `DUSTPAN_AUDIT_WEBHOOK_SECRET_FILE` names.
Only the owner of the file can read it.
Each request carries two headers:

- `Dustpan-Timestamp`: the time of the request in Unix seconds.
- `Dustpan-Signature`: `v1=` and the hex HMAC-SHA256 of the timestamp, a `.`, and the body.

The receiver computes the signature with the same key and compares it in constant time.
Refuse a request whose timestamp is more than 5 minutes old.
A 2xx answer means the receiver holds the batch.
The server does not follow a redirect: a 3xx answer is a failed batch, and the server sends the batch again to the same URL.

### OTLP logs

`otlp:<https URL>` sends the entries to an OpenTelemetry collector or to any receiver of OTLP logs over HTTP with the JSON encoding.
The URL is the base address of the receiver, for example `otlp:https://collector.example:4318`.
The server adds the path `/v1/logs`, unless the path of the URL already ends with it.
A query in the URL stays.
The URL must use HTTPS and must not hold a user name or a password.
The server sends only to a public address and does not follow a redirect.
The start fails when the URL names a blocked address, for example a loopback or metadata address.
A connection to a blocked resolved address fails, and the sink status shows the failure.
For a receiver on a private network, set `--audit-sink-allowed-ranges`.

Each request is one `ExportLogsServiceRequest` with the header `Content-Type: application/json`.
It holds one resource with the attribute `service.name` set to `opendustpan`, and one log record for each entry:

| OTLP field | Value |
| --- | --- |
| `timeUnixNano` | The time of the entry, in nanoseconds, as a string. |
| `severityNumber`, `severityText` | `9` and `INFO` for an audit entry. |
| `body` | The message as a `stringValue`: `audit entry`. |
| `attributes` | The other fields of the entry, in order: `scope`, `sequence`, `atMs`, `subject`, `action`, `object`, `kind`, `outcome`, `prevHash`, `hash`, and the fields that the entry has. A string is a `stringValue`, a whole number is an `intValue`, and `changes` is a `kvlistValue` or an `arrayValue`. |

Each entry carries `scope` and `sequence`, as in the other sinks.
A receiver can check the chain and drop duplicates with them.

To send a header with each request, for example an API key, set `DUSTPAN_AUDIT_OTLP_HEADERS`, or put the text in a file that `DUSTPAN_AUDIT_OTLP_HEADERS_FILE` names.
Only the owner of the file can read it.
No flag takes the headers, because flags show in process lists, and the sink list never shows them.
The text is a list of `Name=Value` items, separated by commas or line breaks, as `OTEL_EXPORTER_OTLP_HEADERS` writes it.
Percent-encode a value that holds a comma, a space or an equals sign, for example `Authorization=Bearer%20<token>`.
The server refuses these headers at start:

- A name that is not a valid header name.
- A name that the server sets itself, such as `Content-Type`.
- A name that you give twice.
- A value with a control character.

An error never shows a header value.
The headers are optional: a collector in a private network can need none.

A 2xx answer means the receiver holds the batch.
A 2xx answer with a `partialSuccess` that rejects records counts the rejected records as skipped (`dustpan_audit_sink_skipped_total`), because a new try cannot deliver them.
Any other answer is a failed batch, and the server sends the batch again with a longer wait each time.
The server does not follow a redirect, so a header never goes to another host.
The logged error text is a fixed class of the failure, for example `write: refused by the target`. It does not hold the status of the receiver or the answer of the receiver.

### Object storage

`blob:<prefix>` writes to the blob store of the server: the directory of `--blob-dir` or the bucket of `--blob-store`.
The prefixes `namespaces` and `pulumi` hold state, so the server refuses them.
The prefix `audit-checkpoints` holds signed audit checkpoints, so the server refuses it.
Each object is `<prefix>/<namespace>/<yyyy-mm-dd>/<first>-<last>-<random>.jsonl.gz`.
The two times are the first and the last entry of the file in Unix milliseconds.
A key holds the characters `a-z`, `0-9`, `.` and `-` only.
Each other character of the name (the namespace, or `@server` for the installation), and `_`, becomes `_` and two hexadecimal digits: `acme` stays `acme`, and `@server` becomes `_40server`.
To keep objects that nobody can delete, use a bucket with S3 Object Lock.

### A file and log rotation

The server opens the file again on `SIGHUP`.
A rotation tool moves the file away and sends `SIGHUP` to the server.

### Alert on a sink that falls behind

`dustpan_audit_sink_entries_total{sink="<name>"}` counts the entries that a sink accepted.
`dustpan_audit_sink_failures_total{sink="<name>",reason="<reason>"}` counts the failed deliveries.
The reason is `size` for an entry that the sink refused because it cannot hold it. An example is a syslog record that cannot fit below `max-size`.
The reason is `unreachable` when the sink never reached its receiver.
The reason is `write` for any other failed write.
The reason is `read` for a failed entry, cursor or namespace read.
The reason is `lease` for a failed sink lease take.
A refused entry logs one error line per entry with the sink name, its `scope` and its `sequence`.
Any other failure logs one error line per round.
The line has the sink name, the reason, and the sequence of the first entry of the failed batch.
A round that knows no sequence leaves it out.
A round that never read a batch, for example a failed lease take or a failed entry, cursor or namespace read, carries no sequence.
A failed batch loses no entry, but a count that keeps growing means the sink falls behind.
A refused entry never arrives: alert when the `size` series grows.
`dustpan_audit_sink_skipped_total{sink="<name>"}` counts the entries that a sink cannot hold, for example a syslog record that cannot fit below `max-size`.
A skipped entry is not in the sink: alert when that count is above zero.
`dustpan_audit_sink_lag_seconds{sink="<name>"}` holds the age in seconds of the oldest entry the sink has not delivered yet, or 0 when the sink is up to date.
`dustpan_audit_sink_lag_entries{sink="<name>"}` holds the count of entries the sink has not delivered yet, or 0 when the sink is up to date.
Alert when either lag keeps growing: the sink falls behind.
The metrics count 16 sinks at most. With more sinks, the server logs a warning at start.

### After a restore

A restore resets the cursor of every sink.
Each sink then receives the restored log again from its first entry.
The restored log can use a sequence again that a sink already holds for an entry from after the backup.
Keep the copies of the sink from before the restore apart, for example by the time of the restore.

```yaml
groups:
  - name: dustpan
    rules:
      - alert: DustpanAuditSinkFailing
        expr: increase(dustpan_audit_sink_failures_total[15m]) > 0
        labels:
          severity: warning
        annotations:
          summary: An OpenDustpan audit sink refuses entries
      - alert: DustpanAuditSinkRefusedEntry
        expr: increase(dustpan_audit_sink_failures_total{reason="size"}[15m]) > 0
        labels:
          severity: critical
        annotations:
          summary: An OpenDustpan audit sink refused an entry it cannot hold
      - alert: DustpanAuditSinkLagging
        expr: dustpan_audit_sink_lag_entries > 50
        labels:
          severity: warning
        annotations:
          summary: An OpenDustpan audit sink falls behind
```

## What this guide does not cover

This guide does not cover these items.

- Retention.
- The full catalog of events.
