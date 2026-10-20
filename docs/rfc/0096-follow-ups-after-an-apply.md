---
rfc: 96
title: Follow-ups after an apply, which start an AWX job template or send a signed webhook with selected outputs
status: draft
created: 2026-10-20
---

# RFC 96. Follow-ups after an apply, which start an AWX job template or send a signed webhook with selected outputs

Status: draft. The owner has decided nothing in this RFC. No code exists for it. The
owner answers the questions at the end (ADR 15).

## Problem

Many teams configure machines after the infrastructure exists. An apply
makes a VPC and three hosts. Then an Ansible playbook configures the hosts.
Today a person starts the playbook by hand, or a CI script reads the
outputs and calls AWX. The script keeps an AWX token in CI, and nothing
records which apply started which AWX job.

The goal: after a stack or a workspace applies, OpenDustpan starts one
external job with selected outputs, and records the result next to the
apply. The first kind starts an AWX (or Ansible Automation Platform) job
template. The second kind sends a signed webhook.

**Scope limit.** A follow-up starts a job, records the result and shows it.
It does not chain jobs, wait for one job before it starts the next, branch
on a result, or cancel an external job. OpenDustpan is not an orchestrator.
AWX workflows already do that work.

**Owner decision to respect.** OpenDustpan never pulls Puppet, Ansible or
Chef facts into its inventory. Their facts can be data sources of a unit.
A follow-up sends data out. It never reads the inventory of AWX back.

**Names (proposal, question 1).** The glossary lists "Hook" as a word to
avoid (decision point), and RFC 95 calls "hook" a Pulumi word. "Webhook"
is on the _Avoid_ list of _External adapter_. This RFC uses **follow-up**
for the stored rule and **follow-up call** for one attempt series against
one target. AWX and Ansible Automation Platform are systems that a
follow-up calls. They are not part of OpenDustpan.

## What is decided already

- **ADR 145 and ADR 149: notifications.** One outbox, written in the
  transaction of each change. A signed webhook sink with HMAC-SHA256 over
  `<id>.<timestamp>.<body>`, two active secrets for rotation, at-least-once
  delivery, claims with a lease, retries from 30 seconds to 1 hour for 15
  attempts, `Retry-After`, and redeliver. Sink secrets are sealed secret
  items, answered once. A change of a sink suspends its subscriptions. A
  notification never holds state or a secret (ADR 145 decision 4), so a
  notification cannot carry outputs.
- **ADR 129 decisions 25 to 28: egress.** `pkg/egress` and `egresshttp`
  check each URL and each resolved address. An installation allowlist of
  hosts and CIDR ranges (one port each) is the only way to reach a private
  address. An always-denied list (link-local, cloud metadata, multicast)
  has no override. `--egress-public-only` ignores each CIDR entry. Today
  the allowlist covers model calls only.
- **ADR 226 decision 10: external checks.** A signed request before a
  decision, with a signed callback. That is a fact producer before an
  apply. A follow-up comes after the apply and decides nothing.
- **ADR 100, ADR 101: jobs.** A job has fixed phase kinds (`plan`, `apply`,
  `deliver`, `verify`). A person's own CLI or CI also writes state, with
  no job.
- **ADR 13, ADR 12.** Shared code names a stack or a workspace a unit. Copy
  keeps each tool's words.
- **ADR 210.** Routes under `/api/dustpan/v1/`, actions `<area>:<verb>`,
  `Idempotency-Key` on each mutating route.
- **ADR 250.** Every input from outside the process is untrusted: tool
  state (so outputs), AWX answers, webhook answers, DNS.

## ADRs this RFC would amend

- ADR 129 decision 25: the egress allowlist would cover follow-up targets
  too, as a second list or with a purpose on each entry (section C).
- ADR 145 decision 5: two new notification types, `unit.applied` (section
  A), so subscriptions get the same signal, and `follow-up.failed`
  (section C).

## Facts from primary sources

Checked on 2026-10-20 against the AWX source on `devel` (commit
`6fc51b6d25b5bf3ad7efa66cc4e62bdb27177027`, 2026-10-15), and the Red Hat
documentation of Ansible Automation Platform 2.6.

**Start a job template.** The AWX route says `launch`. This RFC says "start".

- `POST /api/v2/job_templates/{id}/launch/` on AWX
  (`awx/api/urls/job_template.py`, line 29). Through the platform gateway
  of Ansible Automation Platform 2.5 and later, the controller API is under
  `/api/controller/v2/`
  (<https://docs.redhat.com/en/documentation/red_hat_ansible_automation_platform/2.6/develop-assembly_controller_api_auth_methods>).
- A successful start answers `201` with `job` (the job ID), a `Location`
  header, and `ignored_fields` (`awx/api/views/__init__.py`, lines 2795 to
  2812). A missing password answers `400`, a template that cannot start
  `405`, and an inventory or credential that the user cannot use `403`
  (`awx/api/templates/api/job_template_launch.md`).
- Fields accepted at start (`JobLaunchSerializer` in
  `awx/api/serializers.py`, lines 4873 to 4890): `extra_vars` (JSON),
  `inventory`, `credentials`, `limit`, `execution_environment`, `labels`,
  `forks`, `job_slice_count`, `timeout`, `instance_groups`, and more.
- AWX accepts a field only when the template asks for it on start
  (`ask_limit_on_launch`, `ask_inventory_on_launch`, and so on). Else AWX
  ignores the field and lists it in `ignored_fields`. An extra variable is
  accepted when `ask_variables_on_launch` is set, or when an enabled survey
  names it and the value passes the survey rules. Else AWX rejects it into
  `ignored_fields` (`awx/main/models/mixins.py`, lines 255 to 300). So a
  start can succeed and still drop the variables that OpenDustpan sent.
- A `GET` of the same start route tells whether the template can start
  with no input: `can_start_without_user_input`,
  `variables_needed_to_start`, `passwords_needed_to_start`,
  `inventory_needed_to_start`.

**Job status.** `GET /api/v2/jobs/{id}/`. The `status` values are `new`,
`pending`, `waiting`, `running`, `successful`, `failed`, `error` and
`canceled` (`awx/main/models/unified_jobs.py`, lines 82 to 91). The last
four are terminal. AWX has no idempotency key on start: two start
requests make two jobs.

**Authentication.**

- AWX `devel` has session, basic and JWT authentication only
  (`awx/settings/defaults.py`, line 376). The JWT comes from the platform
  gateway. AWX `devel` has no OAuth2 token routes of its own any more.
  Basic authentication is on by default (`AUTH_BASIC_ENABLED = True`, line
  402).
- On Ansible Automation Platform 2.6, a person creates an OAuth2 token at
  `POST /api/gateway/v1/tokens/` and sends it as
  `Authorization: Bearer <token>`. An administrator can turn off basic
  authentication of the gateway (the documentation page above). The
  scopes of a token are not on that page (question 7).

**Callbacks and receivers.**

- The provisioning callback `POST /api/v2/job_templates/{id}/callback/`
  takes a `host_config_key` and limits the job to the host that sends the
  request, found from its source address
  (`awx/api/templates/api/job_template_callback.md`). The server of
  OpenDustpan is not that host, so the callback does not fit.
- AWX webhook receivers exist for GitHub, GitLab and Bitbucket Data Center
  only (`awx/api/urls/webhooks.py`). Each expects the payload and the
  signature header of that forge. To use them, OpenDustpan would have to
  pretend to be a forge. This RFC rejects that.
- An AWX notification template of the type webhook can `POST` or `PUT` to
  a URL when a job ends, with custom headers and basic authentication
  (`awx/main/notifications/webhook_backend.py`). It follows up to 5
  redirects and has no HMAC signature. A callback into OpenDustpan would
  need a new inbound route (option D2).

**Jinja in extra variables.** The AWX setting `ALLOW_JINJA_IN_EXTRA_VARS`
is `template` by default: Jinja is allowed only in the variables of the
saved template, not in variables given at start. The other values are
`always` and `never`. Its help text says that Jinja in start variables
"can use Jinja2 templates to run arbitrary Python" (`awx/main/conf.py`,
line 319, and `awx/settings/defaults.py`, line 503). OpenDustpan cannot read
this setting without an AWX administrator token. So an output value with
`{{` reaches a template engine on an AWX installation set to `always`.

## Options and recommendations

### A. Which change fires a follow-up

- **A1. Job states only** (`job.succeeded` of an `apply` job). This misses
  each apply from a person's own CLI or CI, which is most applies today.
- **A2. Each final state change of a unit**, whatever wrote it: a final
  state version of a workspace, or a Pulumi update `up` or `destroy` that
  succeeded. One signal, `unit.applied`, in the transaction of the write.
  It names the unit, the state version serial or the update version, and
  the job ID when a job made the change.
- **A3. A change order** (ADR 190) that applied all its units: one call
  with the outputs of each unit.

**Recommendation: A2 first, A3 later (question 3).** `unit.applied` is also
a new notification type, so a subscription can use it. Its dedupe key is
the unit ID and the serial or the update version, so a retried write makes
one signal. A follow-up selects units by a filter: one unit, a collection,
or `opentofu:shop/*`, with the same matcher as a subscription filter. A
`pulumi preview`, a plan, and a `refresh` fire nothing.

### B. Where follow-ups live

- **B1. A new sink kind** `awx` of ADR 145. A sink would then send state,
  against ADR 145 decision 4.
- **B2. A new object that reads the outbox.** A follow-up has its own
  cursor on `unit.applied` notifications, its own call table, and reads the
  outputs of the named state version when it sends. It reuses the claim,
  retry, egress and signing code of `pkg/notify`.
- **B3. A job phase** `follow-up`. This covers jobs only, and a new phase
  kind changes ADR 100.

**Recommendation: B2.** A notification stays free of state. The outputs
that a call sends come from the exact version that fired it, not from the
latest version.

### C. Delivery and egress

**Durable, at least once.** Each matching follow-up and `unit.applied`
makes one call, with the ID prefix `fuc-`, as a durable operation with a
claim and a lease (ADR 145 decision 17). The same follow-up and the same
signal never make a second call.

**Order.** For one follow-up and one unit, calls start in serial order. A
call waits while an older call of the same pair is not terminal. Option:
a pending call that a newer one passes becomes `superseded` (question 4).
The recommendation: no coalescing in the first slice.

**Retries.** The schedule of ADR 145 decision 18. Retry only when the
target cannot have acted: a refused connection, a failed TLS handshake,
`429`, or `503` with `Retry-After`. A timeout after the request was sent,
`502` and `504` make the outcome `unknown`, because AWX has no idempotency
key. An `unknown` call does not retry by itself. A person redelivers it
after a check in AWX. `egress.ErrRefused` and other `4xx` stop at once
(`abandoned`).

**Dead letter.** An `abandoned` or `unknown` call stays in the call
history, sends the notification `follow-up.failed`, and can be redelivered
by a person. A follow-up whose calls all failed for 24 hours is suspended,
as ADR 145 decision 19 does for a subscription.

**Limits.** Connect 10 seconds, request 30 seconds, answer body at most
`egress.MaxBodyBytes`, no redirect, no proxy. At most 50 mapped outputs, 16
KiB for each value and 64 KiB for each payload. At most 100 follow-ups for
each namespace.

**Egress.** AWX is often on a private address, which the notification
sender refuses. The options:

- **E1. Public addresses only**, as for a webhook sink. Most AWX
  installations cannot be reached.
- **E2. The installation allowlist of ADR 129**, with a purpose on each
  entry (`model`, `follow-up`). The operator owns it
  (`installation:manage-settings`). The always-denied list and
  `--egress-public-only` apply unchanged.
- **E3. E2 and a target list of the namespace.** A target must match an
  entry of the installation (purpose `follow-up`) and an entry of the
  namespace list. Each list stops a target alone.

**Recommendation: E3.** On a multitenant installation, the operator keeps
`--egress-public-only` on, so no tenant reaches a private address. The URL
check runs at the save, at the test, and before each attempt. The dial
guard checks each resolved address.

### D. The AWX kind

**Configuration.** Base URL (`https`), API path (`/api/v2/` or
`/api/controller/v2/`), job template ID, the output mapping, an optional
fixed `limit` and an optional fixed inventory ID. OpenDustpan sends
`limit` and `inventory` only when the follow-up sets them.

**Credential.** One OAuth2 token of the platform gateway, or a user and a
password where only basic authentication exists (question 7). It is a
secret item sealed for the follow-up, in the way of ADR 149 decision 10.
The API answers it never, also not at the create, because the person typed
it. A change of the target, the credential or the mapping suspends the
follow-up, and a person resumes it, as ADR 149 decision 12 does for a sink.

**Output mapping.** An explicit list of pairs `{output, variable}`. No
wildcard, no "all outputs". A variable name matches `^[a-z_][a-z0-9_]{0,63}$`
and is not a name that Ansible reserves (for example `ansible_*`,
`hostvars`, `inventory_hostname`). OpenDustpan adds two variables of its
own: `dustpan_call_id` and `dustpan_unit`. `extra_vars` goes as a JSON
object, never as a YAML string.

**Sensitive outputs.** An output that the tool marks as sensitive (an
OpenTofu `sensitive` output, a Pulumi secret) is never sent. The save
refuses a mapping that names an output that is sensitive in the latest
version. Before each send, the call checks the version that fired it
again. When a mapped output is sensitive or missing there, the call fails
with `output-refused` and sends nothing. The issue asks for an opt-in to
send secret values. This RFC does not propose one (question 2).

**Start and watch.**

1. Before the start, a `GET` of the start route. When
   `can_start_without_user_input` is false, the call fails with
   `awx-needs-input`, and lists the names of the missing variables.
2. `POST` the start. The call records the AWX job ID.
3. When `ignored_fields` is not empty, the call records the field names
   (names only) and ends `failed` with `awx-fields-ignored`. The AWX job
   still runs. Option: `warn` instead of `failed` (question 5).
4. The call reads `GET /jobs/{id}/` every 15 seconds, then doubles the wait
   up to 5 minutes, until a terminal `status` or the end of its watch time
   (default 6 hours, from 1 minute to 24 hours). It stores the status, the
   start and the end times, and a link to the job in AWX.

**Option D2: a callback** from an AWX notification template into a new
inbound route of OpenDustpan. It saves the reads, but it needs an inbound
route with its own token, an AWX template for each team, and no signature.
**Recommendation: polling (D1) in the first slice, D2 later if the reads
cost too much.**

**Result on the record.** The call shows on the job page when a job made
the change, and on the state version of the workspace or the update of the
stack. The console says "workspace" and "state version" for OpenTofu,
and "stack" and "update" for the Pulumi CLI. The apply job ends when its apply ends. A
follow-up never changes the state of a job.

### E. The webhook kind

A `POST` of a JSON body: `schemaVersion` 1, `callId`, `followUpId`, `unit`
(the unit reference and the stable unit ID), `tool`, `serial` or
`updateVersion`, `jobId` when there is one, `at` in Unix milliseconds, and
`outputs` with the mapped values only. Headers: `Dustpan-Call-Id`,
`Idempotency-Key` (the same value), `Dustpan-Timestamp` and
`Dustpan-Signature`.

**Signature.**

- **S1. The scheme of ADR 145 decision 21** with the call ID in place of
  the notification ID, and two active secrets. A receiver tries both. No
  key ID.
- **S2. S1 and a header `Dustpan-Key-Id`** that lists the key ID of each
  `v1=` value, in the same order. The key ID is outside the signature. A
  wrong key ID only makes the check fail.
- **S3. A new scheme** with the key ID in the signed text.

**Recommendation: S2.** `notify.Verify` still checks the signature, and a
receiver with many keys picks the right one at once. Rotation is the
procedure of ADR 145 decision 22.

A `2xx` answer delivers the call. The call records the status code. The
webhook kind has no watch.

### F. Authorization and audit

| Route | Action | Object |
| --- | --- | --- |
| `GET .../namespaces/{ns}/follow-ups[/{id}]` | `namespace:read-follow-ups` | namespace |
| `POST`, `PUT`, `DELETE .../follow-ups[/{id}]`, `.../follow-ups/{id}/resume` | `namespace:manage-follow-ups` | namespace |
| `POST .../follow-ups/{id}/test` | `namespace:manage-follow-ups` | namespace |
| `GET .../follow-ups/{id}/calls[/{call}]` | `namespace:read-follow-ups` | namespace |
| `POST .../follow-ups/{id}/calls/{call}/redeliver` | `namespace:manage-follow-ups` | namespace |
| `GET`, `PUT .../namespaces/{ns}/follow-up-targets` | `namespace:manage-follow-ups` | namespace |

**The creator decides what flows out**, as ADR 145 decision 15 and ADR 149
decision 11 do. The last person who saved or resumed a follow-up is its
creator. Before each attempt, the call checks that the creator still holds
`namespace:manage-follow-ups` and `unit:read` on the unit, with its unit
and collection grants. Else the follow-up is suspended and the call is
`skipped`. So a follow-up never sends an output that its creator cannot
read.

**Credentials.** The options: one action for all (above), or a second
action `namespace:bind-follow-up-credentials`, so a platform team holds
the AWX token and an application team edits the mapping. A change of the
mapping suspends the follow-up in both options. **Recommendation: one
action, with the suspend (question 6).**

**Call history.** A reader with `namespace:read-follow-ups` sees the state,
the class of the last error, the HTTP status, the AWX job ID and status,
the names in `ignored_fields`, and the SHA-256 of the payload. Never the
output values and never the credential.

**Audit.** A create, a change, a delete, a resume, a test and a redeliver
write one entry each, of the kind `change`. A refusal writes one entry of
the kind `denied`: a target outside the allowlist, a refused output, a
suspend for lost access. A delivered call writes no entry. The call
history is its record.

**Gates.** A follow-up starts no gate in this RFC. The apply already passed
the gates of its decision points. Option: a fact `followUps` at
`change.planned`, so a gate can see that an apply starts an AWX job
(question 8).

### G. Defense in depth (ADR 250)

| Input | 1. Check at the boundary | 2. Contain at use | 3. Limit | 4. Authorize at use | 5. Fail closed and audit |
| --- | --- | --- | --- | --- | --- |
| Follow-up document (API) | Allowlist for each field. URL scheme `https`. Variable name pattern. Reserved names refused | Values stored as typed fields, never as text that a template reads | 100 follow-ups, 50 outputs | `namespace:manage-follow-ups` on the route, the store checks the fence | Unknown field refused (`422`). Denial audited |
| Target URL and DNS | `egress.CheckURL` at save | Dial guard on each resolved address. No redirect. No proxy | Connect 10 s, request 30 s | Installation list and namespace list, both before each attempt | Refusal is `abandoned`, `denied` entry |
| Output values (from tool state) | JSON value types only. Size per value. For the AWX kind, refuse a string with `{{`, `{%` or `{#` | `extra_vars` as a JSON object. Webhook body JSON-encoded. Never put in a URL, a header or a log | 16 KiB each, 64 KiB total | Creator holds `unit:read` at send time | Sensitive or missing output: `output-refused`, nothing sent, `denied` entry |
| Credential | Length and character set at save | Sealed secret item, opened in memory for one request. Sent only to the origin of the target | One credential for each follow-up | Store refuses an item sealed for another follow-up or scope | Item that does not open: call fails, nothing sent |
| AWX answer | Parse a fixed schema: `job` as a positive integer, `status` from the eight values, `ignored_fields` keys only | Store parsed fields only, never the raw body. Strip control characters from names | Body at most `egress.MaxBodyBytes`, 100 names of 64 bytes | A job ID is only shown, never used for another call | Unknown status or bad JSON: `unknown`, no retry |
| Webhook answer | Status code only | No body stored, except an excerpt of at most 512 bytes of a non-`2xx` answer, with no control characters and the secret filter applied | 512 bytes | Shown only with `namespace:read-follow-ups` | Non-`2xx`: retry or `abandoned` |

**No template injection.** OpenDustpan renders no template. The mapping is
names to names. A value is data and goes through a JSON encoder. The Jinja
check of the AWX kind is a defense-in-depth layer: it does not rely on the
AWX setting `ALLOW_JINJA_IN_EXTRA_VARS`, and that setting does not rely on
it. Each needs a test that sends `{{ lookup('pipe', 'id') }}` as an output
value.

### H. Overlap

- **RFC 95 (day-two actions).** RFC 95 records actions that a tool runs in
  its own apply. A follow-up is an action that OpenDustpan starts after
  the apply. Both can show on the same apply. The names stay apart.
- **The CI shim (RFC 94).** A team's CI can call AWX itself. A follow-up
  also covers applies from a laptop, and keeps the AWX token out of CI.
- **External checks (ADR 226 decision 10).** Same signature family, other
  side of the apply: a check produces facts before the apply, a follow-up
  acts after it.

## The first slice

**Persona.** The ops team of a retailer runs the AWX job template
`configure-web` (ID 12) after each apply of the workspace
`opentofu:shop/web-prod`. The template asks for variables on start. AWX
is at `https://awx.corp.example` on `10.20.0.15`. Today an engineer copies
the host addresses from `tofu output` into the AWX form.

**What ships.**

1. The notification type `unit.applied`, for OpenTofu final state versions
   only.
2. Follow-ups of the AWX kind only, for one unit or a collection, with
   polling.
3. The purpose `follow-up` on the installation allowlist, and the namespace
   target list.
4. The routes and actions of section F, the call history, the audit
   entries, and the call on the state version page of the workspace in the console.
5. The user documentation page (ADR 214).

The webhook kind, Pulumi stacks and change orders come in later slices.

**The e2e step (first).** In `e2e/`, a fake AWX on `httptest.NewTLSServer`
at `127.0.0.1`, with the server trusting its certificate. It serves the
`GET` and `POST` of `/api/v2/job_templates/12/launch/` and
`GET /api/v2/jobs/42/`, which answers `running` and then `successful`.

1. Put `127.0.0.1/32` with the port of the fake AWX on the installation
   allowlist (purpose `follow-up`) and on the namespace target list.
2. Create a follow-up for the workspace with the mapping
   `web_ips -> web_hosts`.
3. Run the real `tofu apply` against the TFE-compatible API, with a
   configuration that has the outputs `web_ips` and `db_password`
   (sensitive).
4. Check that the fake AWX got one start with a bearer token, and
   `extra_vars` with `web_hosts`, `dustpan_call_id` and `dustpan_unit`
   only.
5. Check that the call history shows the AWX job `42` as `successful`.
6. Negative steps:
   - A mapping of `db_password` fails at save.
   - An output value with `{{` ends `output-refused`, and the fake AWX
     gets nothing.
   - The server refuses a target outside the namespace list, and writes a
     `denied` entry.
   - A fake AWX answer with `ignored_fields` ends `awx-fields-ignored`.

## Open questions

1. Is "follow-up" the right name? Other options: "apply trigger",
   "post-apply call".
2. May a follow-up ever send a sensitive output, with an opt-in that the
   issue mentions? This RFC says never.
3. Does a follow-up for a change order (one call with all units) come in a
   later slice, or never?
4. When applies come faster than calls end, do older pending calls of the
   same unit become `superseded`, or does each call go out in order?
5. Do ignored AWX fields fail the call (this RFC) or only warn?
6. One action for follow-ups and their credentials, or a second action to
   bind a credential?
7. Which credential types does the AWX kind accept: a gateway OAuth2
   token only, or also a user and a password for AWX with basic
   authentication? Which token scope does a start need? This needs a
   check against a running Ansible Automation Platform 2.6.
8. Does a gate see, through a fact at `change.planned`, that an apply will
   start follow-ups?
9. Do follow-up targets get a CA bundle of their own for an AWX with an
   internal CA, or does the server trust store cover it?
10. How long does the server keep call history: 30 days as for deliveries,
    or 400 days as for stored plans?

## Proposed glossary lines

- **Follow-up**: a stored rule of a namespace that starts one external job
  after a unit applies, with selected outputs. Its kinds are `awx` and
  `webhook`. _Avoid_: hook (that is a decision point or a Pulumi word),
  trigger, post-apply action.
- **Follow-up call**: one series of attempts of one follow-up for one
  `unit.applied` signal, with the ID prefix `fuc-`. _Avoid_: delivery
  (that is a notification), run, job (that is ours).

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
