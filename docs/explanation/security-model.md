# Security model

This page says what the OpenDustpan server protects, how, and what it does not
protect. It describes the server as it is on `main` today. A planned change
carries the word "planned".

OpenDustpan is pre-alpha. Do not keep production state on it.

## Sign-in

- **Administrator tokens are the only sign-in.** The server reads them from
  `DUSTPAN_ADMIN_TOKEN` (comma-separated) or from the file in
  `DUSTPAN_ADMIN_TOKEN_FILE`. No flag takes a token, because flags show in
  process lists. The server deletes `DUSTPAN_ADMIN_TOKEN` from its
  environment after it reads it.
- **Each token is at least 32 bytes.** The server refuses an empty or a
  duplicate token, and a token file that the group or others can read.
- **The server keeps no token in its database.** It holds a SHA-256 hash of
  each token in memory and compares in constant time.
- **Each tool sends the token in its own header:** the Pulumi CLI as
  `Authorization: token <token>`, OpenTofu as `Authorization: Bearer <token>`.
- **Two short-lived capabilities need no token.** A Pulumi update uses an
  update lease token. An OpenTofu state upload uses a random token in its
  URL. Each is 32 random bytes, and the server keeps only its SHA-256 hash.
  A running Pulumi update also checks, on each request, the access token that
  started it. When that token is revoked, expired or its principal is deleted,
  each later request answers 401. A request in flight when the revoke commits
  may finish. An update that a bootstrap administrator token started has no
  such token, so no check runs.
- **The console session cookie is `SameSite=Strict`.** A request with the
  session cookie and no `Origin` header is refused when it is a read that the
  browser marks cross-site, and when it can change state without
  `Sec-Fetch-Site: same-origin`. A method that can change state also needs
  the `X-Dustpan-CSRF` header. The sign-in cookie stays `SameSite=Lax`, so
  the return from the identity provider works.

Planned: users, teams and roles, scoped access tokens, and
single sign-on with directory sync.

## Authorization

- **Every token signs in as an administrator,** and an administrator can do
  every action. No other kind of principal exists today.
- **Every route declares its actions.** Before a handler runs, the server
  signs in the caller, finds the object of each action, and checks each
  action. A test in each API fails when a route has no action and is not on
  the explicit list of public routes.
- **Routes that need no sign-in:** `GET /api/capabilities`,
  `GET /.well-known/terraform.json`, `GET /api/v2/ping`, `GET /health/live`,
  `GET /health/ready`, and the files of the web console when you set
  `--console-dir`.

The actions already split, for example, reading a unit from reading its
secrets. With only administrators, that split has no effect yet.

## Access tokens

The server issues access tokens that it stores (`odp_` secrets) for
persons and services.

- **The server stores only a SHA-256 digest of each secret.** It shows the
  secret once, at create or rotation.
- **An access token cannot make or rotate a token.** Only a caller without
  a stored token can: today the bootstrap admin token, later a
  sign-in. A token that could make tokens would outlive its
  maximum lifetime and a revoke of the token that made it.
- **Each person makes only its own personal tokens.** A token acts as its
  principal, so a personal token for another person would be a grant by
  proxy. No principal makes or rotates a personal token of another person,
  an administrator included. A person makes and rotates its own tokens after
  a sign-in, through `/api/server/caller/tokens`. A holder of
  `server:manage-tokens` can revoke the personal token of any person.
- **A service token needs `principal:manage-tokens` on its service
  principal.** A holder can make, rotate and revoke the tokens of that
  principal. So it can act as the principal, with every grant that the
  principal has now or gets later. A token acts with the grants of its
  principal at the time of each request. The design wants this: the grant of
  `principal:manage-tokens` is the decision.
- **A workload trust rule needs `principal:manage-tokens` on its
  principal.** A trust rule lets a workload with a matching token from its
  issuer act as a service principal of the namespace of the rule. So a
  create of a rule needs `namespace:manage-trust-rules` on the namespace
  and `principal:manage-tokens` on the principal, the same right as a
  service token. An access token cannot create a rule: only a person after
  a sign-in can. A rule never names a principal of another namespace, a
  person or a disabled principal, and never names an issuer of this server.
- **A token exchange issues a short-lived token of the principal of a
  rule.** A workload with a token that a trust rule accepts gets an access
  token of the kind `trust` for the principal of the rule. It lives one hour
  by default, no rotation renews it, and it never creates a rule or another
  token. A refused exchange answers the same 401 for each failed check.
- **An account administrator can act as any service principal.** The
  built-in roles `account-administrator` and `site-administrator` hold
  `principal:manage-tokens` on the installation, which holds every
  principal. To let a principal act as one service principal only, give it
  the role `token-manager` on that principal.
- **Only a holder of `server:manage-access` on the installation can give
  `principal:manage-tokens`.** A grant on a principal must name a service
  principal and a role with principal actions only. Otherwise the server
  answers 400.
- **The export of a service principal lists who can act as it.**
  `GET /api/server/principals/{id}/access-review` lists the holders of
  `principal:manage-tokens` on the principal. The audit entry of each grant
  to a service principal names the same holders in the change
  `tokenManagers`. The audit entry of each grant to a group names the same
  holders for each service principal in the group in one change
  `tokenManagers`, with one line per service principal in the form
  `<principal ID>: <holder>, <holder>`. The audit entry of each membership
  add of a service principal names the same holders in the change
  `tokenManagers`. The audit entry of each membership add to a group that
  lets its members act as service principals names, in the change
  `actsAs`, what the new member can act as, one per line, as the access
  review names it: `principal:<id>` for one service principal, the name of
  a namespace for the service principals of that namespace, and `@server`
  for every service principal.
- **A revoke of `principal:manage-tokens` does not end the tokens that
  its holder already made.** Those tokens act as the service principal
  until they expire. After such a revoke, rotate or revoke the tokens of
  the principal.
- **A namespace can have its own service principals.** A holder of
  `namespace:manage-principals` on the namespace makes them, and
  `namespace:read-access` reads them. A principal of a namespace can only
  hold grants on its namespace or on an object inside it: the server
  refuses any other grant with 400, and checks it again in the transaction
  of the grant. It cannot be a member of a group. A holder of
  `namespace:manage-principal-tokens` on the namespace makes, rotates,
  lists and revokes the tokens of the principals of that namespace, through
  `principal:manage-tokens` on each of them. It reaches no other principal.
  A grant of a role with it needs every data action on that namespace, or
  `server:manage-access` on the installation. The access-review export of
  such a principal lists the holders of `namespace:manage-principal-tokens`
  on its namespace.
- **Each namespace has a namespace service principal.** The server uses it
  to start a plan job when no person starts the job, for example for a run
  trigger. Its subject is `system:namespace-service:<namespace>`, and the
  audit entries of such a start show that subject. It holds one action,
  `unit:run-plan`, on the units of its own namespace, and no other action on
  any other object. The server builds it in its own process and keeps no
  record of it, so it is not in the principal list and has no grant row.
  Nobody can act as it. It cannot sign in, and it has no token and no
  session. No API call changes what it holds:
  - The server refuses each principal that has a subject with the prefix
    `system:namespace-service:`, in any case, with 400. This applies to a
    sign-in through an identity provider too.
  - The server gives no grant, no group membership and no token to a
    principal record with such a subject.
  - The authorizer decides on the subject, the server credential and the
    namespace of the unit before it reads any grant. A caller with such a
    subject and any other credential, or with the administrator flag, is
    denied every action.
- **Only a site administrator can change the sign-in settings.** The
  action `server:manage-sign-in` changes the session policy with
  `PATCH /api/server/session-policy`: the idle limit (15 minutes by
  default) and the maximum length (12 hours by default) of a console
  session. Of the built-in roles, only `site-administrator` holds it.
  `account-administrator` does not, so the tenant side of a hosted
  installation cannot change the sign-in settings of the whole
  installation. No sign-in method issues a session yet, so the policy has
  no effect until one is available.
- **The server keeps one site administrator.** The server refuses each change
  that leaves the installation with no person who is a site administrator, and
  answers 409. The changes are: the delete or the disable of the last person
  who holds the `site-administrator` role on the installation, the revoke of
  the last grant of the role, the removal of the last holder from a group that
  has the grant, and the delete of that group. A holder who is disabled does
  not count. A service principal or a workload that holds the role does not
  count either, because every route that makes a site administrator needs a
  fresh console session, and only a person has one. The inactivity disable and
  the expiry of a temporary administrator are not refused: they are policy, and
  the host command in the host recovery guide makes a new administrator.

The audit entry of each token or account that the inactivity disable turns
off names the rule and the period of the token policy, for example "access
token not used for at least 90 days (token policy: inactivity)". A reason that
a caller gives for a manual sweep is kept.

## Pulumi secrets

The Pulumi CLI sends each secret config value to the server to encrypt and
decrypt it. The server uses envelope encryption:

- **One data key for each stack.** It is 256 random bits, and it encrypts
  the values of that stack with AES-256-GCM, a random 12-byte nonce, and the
  stack as associated data. A ciphertext of one stack does not decrypt as
  another stack.
- **The key-encryption key wraps each data key,** also with AES-256-GCM.
  The database holds only wrapped data keys.
- **The server checks the key at start.** When the database holds wrapped
  keys and the key-encryption key is missing or is a different key, the
  server refuses to start.
- **A stack delete deletes its data key.** After that, no old ciphertext of
  that stack can be decrypted.

The values stay encrypted in your stack configuration and in the checkpoints,
because the Pulumi CLI writes only the ciphertext there.

## OpenTofu state

The server encrypts each new OpenTofu state version before it stores it:

- **One data key for each workspace.** The key-encryption key wraps it, as
  for a Pulumi stack.
- **The server encrypts the state blob in chunks of 64 KiB** with AES-256-GCM. The
  associated data of each chunk binds it to its workspace, its position and
  the end of the stream. A changed, reordered or cut-off blob does not open.
- **The server also encrypts the outputs document in the database.**
- **A download answers the same bytes that the CLI sent.** The MD5 and the
  size of a state version stay those of the plain bytes.

### Force-unlock

A force-unlock releases the lock of a workspace whoever holds it. It can make
a running job fail, so it needs the action `unit:write-forced` on the
workspace, on the TFE-compatible API and on the OpenTofu http backend. Only
the built-in role `admin` has this action. A person with the role
`maintainer` gets 403, and the message names the action. An administrator
can give the action to an on-call group with a custom role.

A force-unlock of a state that the client encrypted writes no fence version,
because the server cannot set the lineage of such a state. It needs the
action and the forced-write switch of the namespace and of the workspace. With a
switch off, the release answers 409, and the message names the action and the
switches.

Each force-unlock writes one audit entry of the kind `change`. It holds the
released lock holder, the lock time, the new state version and the reason,
if the caller gave one.

### State that the client encrypts

OpenTofu can encrypt its state on the client before it sends it. The server
then stores the state, but it cannot read it. This keeps the state secret from
the server, and it costs you every check and view that needs the content of
the state:

- **No check of the content.** The server checks only the encryption envelope
  and the plain `serial` and `lineage`. It cannot see a broken or truncated
  state inside the envelope, so it stores that state as it is. Only the tool
  that holds the key finds the fault, at the next run.
- **Gates see no resources.** For a write or a restore of such a state,
  `context.state.available` is `false`, and `state.resources` and
  `state.removed` keep their zero values. A gate that reads them never
  matches. The write goes on under the other gates, and the decision record
  says `state is client-encrypted`.
- **No outputs.** A read of the outputs answers 409, and other units cannot
  read them through output sharing.
- **Less history.** A diff of two versions answers 409 when either one is
  encrypted. History and point-in-time views show the version with no
  resources.
- **A force-unlock writes no fence version** (see above).

Use client encryption when the server must not hold the plain state. For the
checks and views above, use the encryption that the server applies to every
state version (see "OpenTofu state").

## The key-encryption key

`serve` needs a key-encryption key before it opens the database. It takes
the first of these:

1. `DUSTPAN_KEY_ENCRYPTION_KEY`: the base64 text of 32 random bytes. The
   server deletes the variable from its environment after it reads it.
2. The key file in `--key-encryption-key-file`
   (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`). There is no default file. Make one
   with `opendustpan key generate --out <path>`: it writes 32 random bytes
   from the system source in base64, with mode 0600, and it refuses a file
   that exists. The server refuses a key file that the group or others can read, and a key
   file that `opendustpan dev` made.
3. No key: the server stops with an error that names the settings. It makes
   no key and it asks no question.

The server never writes a key file. Each start logs the path of the key
file, never the key. When the key file is in the directory of the SQLite file
or of the blobs, each start also logs a warning, because a backup of the data
then holds the key. No flag takes the key itself.

The database and the backups hold no copy of the key. A lost key means lost
secrets and lost OpenTofu state: nobody can decrypt them. A copy of the data directory that holds
the key file also holds the key, so keep the backups and the key
apart.

Planned: a command that rotates the key-encryption key, and keys
that a key management service holds.

## What the server encrypts at rest

The server encrypts only the data in the table below. It does not encrypt
the database or the blob store as a whole. Protect both, for example with
disk encryption and with access rules on the S3 bucket.

| Data | Where | Encrypted by the server |
| --- | --- | --- |
| OpenTofu state, with every sensitive value | Blob store, and the outputs in the database | Yes. Versions that an older server wrote stay plain: the server does not encrypt them later |
| Pulumi checkpoints | Blob store, compressed | No. The secret values in them are ciphertext from the Pulumi CLI |
| Pulumi secret config values | Your stack configuration | Yes, as above |
| Pulumi update events, for example the lines that a program prints | Database | Yes, each event with the key of its update. A printed line can hold a secret value in plain text, because the Pulumi CLI masks only the secret values that it knows |
| Wrapped data keys | Database | Yes, by the key-encryption key |
| Audit entries, with no secret value | Database | No |
| Backups | Backup target | Not as a whole. A backup holds the stored bytes: encrypted state stays encrypted. It holds no key-encryption key |

The server creates the blob directory with access for its owner only.

Encryption of a whole backup is not decided.

## Integrity

- **OpenTofu state: MD5.** The protocol of OpenTofu names each state version
  by its MD5. The server refuses an upload whose size or MD5 differs from
  what the client declared, and checks both again on each copy. MD5 finds
  accidental damage, for example a cut-off upload or a bad disk. It does not
  stop tampering: a person who can write the blob store can make a changed
  state with a matching MD5.
- **Pulumi checkpoints: SHA-256.** The server stores the SHA-256 of each
  checkpoint and checks it on each read.
- **Backups.** A restore checks the manifest, the dialect and the schema
  version, the SHA-256 of the SQL data, the key, and the size and MD5 of each
  blob. It writes nothing before the key check passes, and it compares the
  row counts at the end.

## Transport

- **TLS.** `--tls-cert` and `--tls-key` turn on HTTPS, with TLS 1.2 or later.
  On plain HTTP on an address that is not loopback, the server logs a warning
  and serves anyway. OpenTofu needs HTTPS.
- **Behind a reverse proxy.** By default the server reads no forwarding
  header. Set `--public-url` to the external URL, and `--trusted-proxies`
  when the proxy adds one. The next section says how the server reads it.
- **Browsers.** Only origins in `--allowed-origins` get CORS headers.
- **Response headers.** When the server serves the console (`--console-dir`),
  each console response carries these headers. The API responses do not.
  - `X-Content-Type-Options: nosniff`.
  - `X-Frame-Options: DENY`.
  - `Content-Security-Policy`. The default is below.
  - `Strict-Transport-Security: max-age=31536000`, only when the server
    serves TLS (`--tls-cert`). The server never sends it on plain HTTP.

  ```text
  default-src 'self'; connect-src 'self' <each origin in --allowed-origins>;
  img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none';
  base-uri 'none'; form-action 'self'
  ```

  The `connect-src` list follows `--allowed-origins`. A console that calls an API on another origin needs that origin in `--allowed-origins`.

  Each header has a setting (`--console-content-type-options`,
  `--console-frame-options`, `--console-csp`, `--console-hsts`). A value
  replaces the default. The value `off`, in any case, sends no header. A value with a line
  break or a character that is not printable ASCII stops the start. If a
  reverse proxy sets the same header, turn the server header off, or make the
  proxy replace the header and not add a second one. The server headers and
  the proxy headers are separate layers: keep both when you can.

## Client address and trusted proxies

The server uses the client address of each request for the request log, the source of each audit entry, and limits.
By default the address is the address of the TCP peer, and the server reads no forwarding header.
With `--trusted-proxies` the server reads only the header that `--trusted-proxy-header` names: `x-forwarded-for` (the default) or `forwarded`.
It ignores the other header, so the proxy needs no step to strip it.
The proxy must overwrite or append the named header on each request it forwards.
The server walks the header from the right and takes the first address outside the trusted list.
A value that a client prepends never wins over the value that the proxy added.
List proxies narrowly, by address where you can, not by a wide network that clients can use.
The trusted list is for proxies only: a direct internal client is not a proxy and must not be on the list.
The server refuses to start with a list that covers every address or that holds host bits: write the network or one address.
A header that the server cannot read gives an unknown address, never the address of the proxy.
A trusted peer that sends no forwarding header also gives an unknown address, and the server writes a warning.
A wrong setup would otherwise make every client read as an internal address.
Allowlists and limits treat an unknown address as denied.
A wrong list records wrong audit sources, so review the list when entries name addresses that you do not expect.

## IP allowlists of a namespace

IP allowlists are off by default.
The operator turns them on with `--ip-allowlists`.
Each namespace opts in with its own list through three routes: GET, PUT and DELETE on `/api/server/namespaces/{namespace}/ip-allowlist`.
These routes need the action `namespace:manage-ip-allowlist` on the namespace.
The action is in the administration family.
Only a grant on the installation holds it.
A namespace admin can not read the list.
A namespace admin can not change the list.
The lists apply to requests that act on a namespace, a stack or a workspace.
Installation-scope routes are not gated by a namespace list.
Backup, restore, maintenance, the access review export, metrics, and the principals, groups, roles and tokens routes run with no namespace check.
A request whose client address (see the section about the client address) is outside the list of the namespace of its object gets 403.
The body is the generic text `forbidden`.
It names neither the list nor the namespace.
An unknown address is denied.
The bootstrap admin token is exempt unless `--ip-allowlists-include-bootstrap` is set, so an administrator cannot lock themselves out.
Every change is audited in the chain of the namespace in the same transaction.
The check runs in the authorization step of every route that calls Authorize.
The Pulumi update-lease routes and the OpenTofu upload-capability routes carry no principal.
They get an address-only check against the list of the namespace of the stack or workspace.
The bootstrap exemption does not apply to them.
For the OpenTofu uploads the check runs after the server checks the capability token and before it reads the body.
For the Pulumi lease routes the check runs after the server checks the lease token of the update.
A caller without the token gets the same answer for a namespace with and without a list.
A caller can not learn the list.
`::/0` matches IPv6 clients only.
`0.0.0.0/0` matches IPv4 clients only.
The server reads an IPv4-mapped IPv6 client address as its IPv4 address first.
A TFE route that lists units can answer 404 for a namespace that the caller may not see.

### Recovery

By default the bootstrap admin token is exempt: it can read, set and delete every rule from any address.
With `--ip-allowlists-include-bootstrap` the lists apply to it too.
A rule that holds none of the addresses that the operator uses locks the namespace.
The server refuses the REST calls of that namespace from outside the list, including the DELETE of the rule.
To recover, restart the server without `--ip-allowlists-include-bootstrap`.
The bootstrap admin token is exempt again.
Delete the rule with `DELETE /api/server/namespaces/{namespace}/ip-allowlist`.
Then start with the flag again if wanted.
When the server has no bootstrap admin token, the exemption never applies.
To recover, restart the server without `--ip-allowlists`.
Delete the rule with `DELETE /api/server/namespaces/{namespace}/ip-allowlist`.
Then start with the flag again if wanted.
To stop the whole feature, restart without `--ip-allowlists`.
The rules stay stored and do not apply.

## Logs

The server writes one log line for each request: the method, the route
pattern, the status, the duration, the size, a request ID, the API and the
signed-in subject. It does not write the path, the body or any token, so the
upload capability in a path stays out of the log. Tests send known values
through each API and check that the log and the metrics do not hold them.

## Limits

- Each request body has a size limit. The Pulumi CLI checkpoints have a
  limit of 32 MiB and OpenTofu states a limit of 32 MiB by default. The
  [configuration reference](../reference/configuration.md) lists each limit.
- Large bodies of the Pulumi CLI and of OpenTofu share 128 MiB at the same time. Over that, the server
  answers 503 with `Retry-After`. An upload with large outputs asks for more budget as the outputs grow. It answers 503 with a Retry-After of 1 to 3 seconds when the budget has no room. It answers 413 when the upload can never fit the budget. Raise `--max-body-bytes-in-flight` to fit such an upload.
- The server waits at most 10 seconds for the request headers.
- **Failed token lookups have a limit for each client address.** The server counts the failed lookups of secret-shaped token values for each client address. The address is the resolved client address. An IPv6 address counts by its /64 network. Behind a proxy that is not in `--trusted-proxies`, every client shares the address of the proxy. A request with an unknown address counts in one shared bucket named `unknown`.
- **Sign-in requests have their own limit for each client address.** The server counts each start of a console sign-in and each failed sign-in step. The count has its own budget. Token lookups do not use it. Many successful sign-ins from one address slow only the next sign-ins from that address. An example is a team behind one proxy. They never slow a token lookup. The address rules are the same as for failed token lookups.
- No other rate limit exists today. Planned as a future change.

## Outbound connections

The server never phones home. No data about your installation, its users or
its use goes to the project, to a vendor or to any other party that you did
not name. This is also true with no configuration, and no setting, also no opt-in
setting, turns it on. In OpenDustpan Cloud, the project is the operator:
its metrics, logs and traces of the installations that it runs stay in its
own systems.
Metrics and logs are for you, and they stay. You scrape the Prometheus
metrics, and the server sends logs only to the log outputs that you
configure. It connects only to the PostgreSQL database
and the S3-compatible storage that you configure. The commands
`namespace create`, `backup`, `backup check`, `upgrade check` and `restore`
call only the server in `--server`. `health` calls only the listen address of the local
server.

## Audit log

Each namespace has an append-only audit log. No route changes or deletes an
entry. The [audit log guide](../guides/audit-log.md) shows the fields of an
entry and how to read them.

- **What writes an entry.** Each response that gives a secret value in
  plain text writes one entry. The kind of access tells two cases apart:
  - `reveal`: a person asked to see a secret value. The reveal call
    answers the plain value at one masked value path of one version. It
    needs `unit:read` and `unit:read-secrets`, and it writes its entry
    before it answers. The Pulumi CLI also reports a reveal through
    `decrypt/log-decryption` and `decrypt/log-batch-decryption`. A state
    download by a person also writes this kind.
  - `tool-operation`: a tool needed secrets for its own work. The Pulumi
    `decrypt` and `batch-decrypt` routes, the OpenTofu route for one
    sensitive output, and a state download by a service, a workload or a job
    write this kind.
- **The principal decides who a state download is for.** The server cannot
  see who runs a CLI command, but it knows the kind of the principal and the
  credential. A download with a console session or an access token of a
  principal of the kind `person` is a `reveal`. A principal with an unknown
  kind, for example the bootstrap admin token, counts as a person, so the
  server writes the stricter entry. A download with a service token, a token
  from a trust rule or a job token is a `tool-operation`. The channel header
  never decides. The server records a reveal from the Pulumi CLI only when
  the CLI calls a `log-decryption` route. A state download answers
  `Cache-Control: no-store`.
- **Who can read the log.** `GET /api/audit/entries` lists the entries,
  `GET /api/audit/entries/{sequence}` reads one entry and
  `GET /api/audit/check` checks the chain. Each needs the action
  `namespace:read-audit` on the namespace. For the installation scope
  `@server`, each needs `server:read-audit` on the installation instead.
- **A hash chain for each namespace.** Each entry holds the SHA-256 of its
  own fields and of the previous entry. The check finds a changed or a
  deleted entry. It does not stop a person who can write to the database:
  that person can write the entries and all the hashes again.
- **A failed append.** A route that answers a secret value answers 500
  with no value, so no value reaches a client without its entry. The two
  `log-decryption` routes answer no secret value, so they still answer 204.
  In each case the server writes a log line with no secret and adds 1 to
  `dustpan_audit_append_failures_total`. Alert when that counter is above
  zero.
- **Backups.** A backup holds the audit log, and a restore keeps a valid
  chain.

Retention, export and an anchor for the chain outside the database are not
decided.

## Report a vulnerability

[SECURITY.md](../../SECURITY.md) says how to report a vulnerability in
private.
