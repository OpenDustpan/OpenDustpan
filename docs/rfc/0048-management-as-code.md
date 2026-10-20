---
rfc: 48
title: Managing OpenDustpan as code
status: discussing
created: 2026-10-10
---

# RFC 48. Managing OpenDustpan as code

Status: discussing. ADR 270 records the decisions of the project owner: a
CLI and the OpenTofu and Pulumi providers now, from the published API
description, with a version pin before 1.0; unit settings are not kept in the
repository; ownership rules are managed as code through the providers. ADR 270
also records the answers to questions 1 and 4 to 8, which the product manager
decided under the owner's delegation of 20 October 2026. Questions 2 and 3 stay
open, because they need the owner: question 2 asks for a generator dependency,
and question 3 asks for a new repository. This RFC stays `discussing` until the
owner answers both. This change holds no client code. The section "The client
port" holds the contract that the client code must meet when it lands in the
repository that question 3 chooses.

## Problem

Platform teams manage their backend with IaC: the `tfe` provider for HCP
Terraform, the Pulumi Service Provider for Pulumi Cloud. Spacelift has
`spacectl`, env zero and Harness have CLIs. A team that uses OpenDustpan
changes its namespaces, collections, gates, approval rules and ownership rules
in the console or with REST calls. Nobody can review such a change in a pull
request, and nobody can see which setting a script left behind.

Three personas decide the options:

- **A platform engineer at a regulated company**, designed against DORA. The
  engineer needs each change to a control (an approval rule, an ownership
  rule) to be reviewed, attributed to a person and traceable. The engineer
  accepts a slower path for a change that weakens a control.
- **A small self-hosting team.** The team runs one installation and upgrades
  it when it likes. It wants one command to install and no extra service.
- **A team that moves between tools.** It uses OpenTofu for some units and the
  Pulumi CLI for others, and it may move. It wants to manage OpenDustpan from
  the tool it already knows, and it may use both.

Facts on `main` (checked 2026-10-20):

- The tool-neutral REST API is the surface for these objects (ADR 17, ADR
  210). ADR 210 sets the rules: every mutating route takes an
  `Idempotency-Key`, and `PATCH`, `PUT` and `DELETE` take `If-Match` with the
  `ETag` of the object. The routes below do not all follow these rules yet.
- **Collections** have routes to list, create, read and delete. They have no
  update route. A read gives no `ETag`, and no route takes `If-Match`.
- **Approval rules** have routes to list, read, put and delete, and the
  approval default has read, put and delete. A `PUT` without `If-Match` is an
  unconditional upsert. No route reads `If-None-Match`, so a create-only write
  cannot be atomic today.
- **Ownership rules** are stored by the gate package (ADR 225 decision 9). No
  REST route reads or writes them.
- Every write to an approval rule or an approval default with an access token
  is refused with `403` and a plain message. The answer has no
  `mfa.fresh-step-required` code. This is stricter than ADR 212 decision 3,
  which says that only a change that reduces security needs a fresh step, so a
  change that does not reduce security works with an access token. An access
  token never gives a fresh authentication (ADR 227 decision 6).
- The API description (ADR 264, RFC 99) is in design. The package
  `internal/api/apidesc` holds the types and the drift check. No description is
  served yet, and no generator is chosen (RFC 99 question 6).
- `GET /api/dustpan/v1/capabilities` is public and gives `apiVersion`
  (`v1`) and, for a signed-in caller, the installation kind. The server version
  is in `/api/server/upgrade-check`, which needs `server:observe` on the
  installation. A provider token should not need that right, so a client has no
  route today that gives it the server version or the version of the
  description.
- Provider wrappers exist as design (ADR 170 to 179): the module
  `providerkit` builds one provider that works with OpenTofu and with the
  Pulumi CLI from one core and two thin protocol adapters, in a repository of
  its own. The issue asks for the providers "through providerkit".
- Policy objects exist: an approval rule has approvers, a count and
  `freshAuthSeconds` (ADR 225 decision 1). An ownership rule has a scope, a
  mode (`enforce` or `evaluate`), path globs and a count (ADR 225 decision 9).
  A collection is a named set of units in a namespace (ADR 51).

The server work that follows from these facts blocks the first provider
release. Each item is an issue, listed in "Follow-up work".

Non-goals:

- Compatibility with the `tfe` provider beyond what the OpenTofu CLI needs. The
  TFE-compatible API serves the OpenTofu CLI. It is not the surface to manage
  OpenDustpan objects.
- Settings read from files in a repository (ADR 270 decision 2).
- Code from Terraform or from the `tfe` provider. Both are under the BUSL, and
  this project copies no code from them.

## Questions

Each question lists options, a recommendation and a benchmark. The benchmarks
are from public documentation of each product or standard. Check them again at
the time of the decision.

### 1. In which order: CLI, OpenTofu provider, Pulumi provider?

The owner decided that all three come now (ADR 270 decision 1). The open
part is the order of work, because one pull request cannot hold them.

| Option | Gives | Costs |
| --- | --- | --- |
| A. The client and the CLI first, then OpenTofu, then Pulumi | The CLI is the smallest program. It tests the client port with the least code. The e2e suite drives it first. | The persona that wants IaC waits for the second step. |
| B. OpenTofu provider first | Most teams use OpenTofu or Terraform syntax already. | The port is first tested through a plugin protocol, which is a heavier test. |
| C. All three at once | One release. | A large first change, and the port can be wrong in three places. |

**Decided in ADR 270 decision 4: A.** The client port and the pin rule are tool-neutral, and
the CLI exercises them without a plugin protocol. The providers then add only
their protocol adapters (ADR 176). Every endpoint lands with its e2e step first
(`CLAUDE.md`).

Benchmark: HashiCorp keeps `go-tfe`, the client library, apart from the `tfe`
provider and the `tfe` CLI role, and both use the library. The GitHub CLI and
the Terraform provider for GitHub each use a client library of their own. In
each case the client library comes first and tools are thin layers over it.

### 2. A client written by hand, or generated from the description?

**Open. This question needs the owner.** A generator is a new dependency. The
project asks the owner before it adds a dependency (`CLAUDE.md`), and RFC 99
question 6 holds the trial and the license rules. ADR 264 decision 5 says that
the CLI and the providers are generated from the published description. This
RFC does not choose a generator and does not decide this question.

| Option | Gives | Costs |
| --- | --- | --- |
| A. Hand-written client | No generator dependency. Full control of errors, retries and `If-Match`. | A person writes each call. The client can drift from the server, which is the problem ADR 264 solves. |
| B. Generated client from the description, behind the port | Matches the server by construction. A new route costs no call code. | Needs an approved generator and a published description (RFC 99). A generated client has awkward errors, and the port must wrap it. |
| C. The port in the client repository, a small hand-written transport (`Idempotency-Key`, `If-Match`, cursors, error translation), and a generated typed layer behind it | The CLI and the providers depend on the port only, so a later change of the generator touches one adapter. This is the RFC 99 shape of the transport layer. | The same dependency as B. |

**Recommendation: C, and do not start the generated layer before the owner
approves a generator.** Until then, the port contract in this RFC is the
specification, and a hand-written adapter for the three first kinds is enough for
the CLI and for the e2e tests. The hand-written adapter then follows ADR 264
decision 5 only in part, so the owner should say whether a temporary
hand-written adapter is acceptable. If the answer is no, the first client waits
for RFC 99 question 6.

Benchmark: Kubernetes `client-go` has generated clientsets and hand-written
`rest` helpers. The AWS SDKs generate service clients from API models and keep
a hand-written core for signing and retries. Both split the transport from the
generated calls, as option C does.

### 3. Where do the CLI and the providers live?

Facts. A provider is a client program that a workstation or a runner starts. It
must not link the SQL drivers or the key code of the server (ADR 170 decision
3 gives this reason for `providerkit`). `providerkit` is in its own repository
with its own tags. Release archives for OpenTofu and for the Pulumi CLI come
from one binary (ADR 177). RFC 99 proposes the SDKs in `sdk/` of this
repository, with `sdk/go` as its own module.

| Option | Gives | Costs |
| --- | --- | --- |
| A. In this repository, as separate Go modules | One pull request changes the API, the description, the SDK, the CLI and the provider. | The tags and the release page hold many products. A provider release follows the server release. |
| B. One new repository for the CLI and both providers | The CLI and the providers share the client and the release. They have a version line of their own. The module does not hold server code. | An API change needs a pull request in two repositories. |
| C. A repository for each product | Each has its own release page and tag. A registry that expects a repository name by convention can find it. | Three repositories for one client. |

Both A and B keep the client out of the `go.mod` of the server, because a
separate Go module has its own `go.mod`. RFC 99 proposes this for `sdk/go`.
`pkg/` is the server SDK (ADR 35), so client code does not go in `pkg/` in
either option.

**Recommendation: B.** One repository, with the CLI and the provider core in
different modules, and one release that builds the CLI, the OpenTofu archive and
the Pulumi archive (ADR 177). B gives the client a tag line and a release page
of its own, and it makes the rule that the client does not import server code
a rule of the repository, not only of the module. A holds the same client in a
separate module in this repository. A costs the shared tags and the shared
release page, and it gains one pull request for an API change and its client.
Before 1.0, a client release ships on the day of each server minor release,
and it pins that minor version (question 8).

**Open. This question needs the owner**, because the owner decides to create a
repository. One check comes first: the naming rules of the OpenTofu registry
(the repository name of a provider) and of the Pulumi plugin download (the
repository and the release asset names). The result of this check is a
precondition for the choice of the name. If a registry needs a repository for
each provider, option C replaces B, and the shared client module stays.

Benchmark: HashiCorp keeps `terraform-provider-tfe` and `go-tfe` in separate
repositories. Pulumi keeps `pulumi-pulumiservice`, the provider for Pulumi
Cloud, in a repository of its own, with its own release cadence, apart from the
Pulumi CLI. The GitHub CLI is one repository apart from the GitHub servers.

### 4. Which objects come first?

Criteria for an object in the first release. The object:

1. is configuration, not state, not a secret and not a runtime record;
2. has a stable ID and the full create, read, update and delete life cycle in
   the REST API, with `ETag` and `If-Match`, and a create-only write;
3. is a control that the regulated persona needs under review (a policy);
4. has a conflict rule that a client can show: a change by another actor
   gives `409` or `412`, and the server changes nothing.

| Option | Objects | Note |
| --- | --- | --- |
| A. The three of the issue | Collection, approval rule, ownership rule | They meet 1, 2 and 4. Approval rules and ownership rules meet 3. |
| B. A. plus namespace, gate and access limit | A larger set. | Each needs its own life cycle check, and a wrong gate locks a team. |
| C. Collections only | The smallest set. | It leaves out the policy objects that motivate the work (ADR 270 decision 3). |

**Decided in ADR 270 decision 5: A**, as the issue states, after the server
work in the facts above: the collection has no update route, the ownership rule
has no route, and a create-only write is not possible. The next group is: namespace,
grant, gate, access limit and the unit settings. ADR 270 decision 2 puts the
unit settings in the providers. A later RFC orders them. An object that holds a
secret (a connection, a token) is out until question 6 and the secret rules of
ADR 175 have an answer for it.

Benchmark: the Pulumi Service Provider and the `tfe` provider both began with
the organization-level objects (teams, access tokens, stack tags, workspaces)
and added the rest later. The AWS Cloud Control API requires a resource type to
define create, read, update, delete and list before it supports it, which
matches criterion 2. ISO/IEC 27001:2022 control 8.9 (configuration management)
asks that configurations, including security configurations, are documented,
implemented, monitored and reviewed; the policy objects are the ones that this
control covers first.

### 5. How does a team bring an existing object under management?

A team already has collections and rules that a person made in the console.

| Option | Gives | Costs |
| --- | --- | --- |
| A. Import by reference | `tofu import` and `pulumi import` take a reference (kind, namespace, ID). The read puts the server state into the tool state. | The team writes the code by hand, or uses the tool's own code generation. |
| B. A. plus `export` in the CLI that writes the code | Fewer typing errors. The CLI can write HCL and Pulumi YAML from the server state. | More code in the CLI. The output format is an interface to keep. |
| C. Adopt on create | A create of an existing object takes it over. | A silent take-over: no one reviews the object that exists. It breaks the rule that a refused create changes nothing (the port returns `ErrAlreadyExists`). |

**Decided in ADR 270 decision 6: A in the first release, B later, never C.** The port
returns `ErrAlreadyExists` for a create that finds an object, and its message
names the reference and the one command that helps (the import command of
the tool). The import reads the object and records its version, so the first
plan shows the difference between the code and the server.

Benchmark: Terraform has `import` blocks with `-generate-config-out`, and
Pulumi has `pulumi import` with code generation, so A then B is the path that
both tools take. Google Config Connector acquires an existing resource
automatically when a manifest names a resource that exists, which is the risk
of option C. A team can set the `abandon` deletion policy, but the take-over has
already happened. For
a regulated entity, an unreviewed take-over bypasses the change record (DORA
Article 9 names ICT change management; SOC 2 CC8.1 asks that changes are
authorized before they are made).

### 6. How do the CLI and the providers sign in?

An access token is the credential of a script, a pipeline and a provider (ADR
53). The token comes from an environment variable or a file that the tool
reads, never from the code, and never from the state of OpenTofu or Pulumi. A
write that needs a fresh authentication cannot use an access token (ADR 227
decision 6). The approval-rule write that makes a rule weaker is one of those
writes. Today the server refuses every access-token write to gate
configuration, which is stricter than ADR 212 decision 3 (see the facts and the
follow-up work).

| Option | Gives | Costs |
| --- | --- | --- |
| A. The client returns `ErrFreshAuthRequired` and its message names the path of a new sign-in. The write changes nothing. No exemption for any token. | Matches ADR 227 with no new server code. A provider shows the error in the plan or in the apply. | A pipeline cannot weaken an approval rule. A person must do that step. |
| B. A step-up for a person: the CLI opens a sign-in, the person completes MFA, and the CLI gets a short-lived credential that carries the fresh authentication. | A person at a terminal can apply a rule change in one run. This is how a cloud CLI works. | Needs server code and a decision of the owner, because ADR 227 says an access token never gives a fresh authentication. A new credential type is a new design. |
| C. An exemption for a service principal. | A pipeline can change any rule. | It breaks ADR 227 and the control that the regulated persona needs. |
| D. A change of an approval rule goes through a change order that people approve. | The review that the regulated persona wants. | The change of a policy becomes a long process, and the provider cannot show the result in one apply. |

**Decided in ADR 270 decision 7: A now, B in a later RFC, never C.** Under A, a pipeline
can create and tighten approval rules and ownership rules, and a person must
apply the changes that weaken them, with a fresh authentication. The error
code is `mfa.fresh-step-required` once the server work in the follow-up list
is done, and the provider shows it as an error of
the resource, not as a crash. The plan cannot know that a change needs the
step, so the failure comes at the apply, with no partial change: a single
`PUT` of one object either happens or does not. Each client sends a user-agent
that names the client and its version, so the audit entry shows which client
made the change.

Benchmark: AWS lets a policy require MFA for an API call (`aws:MultiFactorAuthPresent`)
and a person obtains a temporary credential with `sts get-session-token` and
an MFA code, which is option B. PCI DSS 4.0 requirement 8.4 asks for MFA for
access to the cardholder data environment and for administrative access, so a
control that needs a person is the expected design. Google Cloud can require
reauthentication for the console and for `gcloud` with its session control.
Option C has no benchmark in these standards. The text of the PCI DSS and
ISO/IEC 27001 controls in this RFC was checked only against secondary sources.
Check it against the standard before the RFC is accepted.

### 7. What happens when the provider state and the server state differ?

Someone can change an object in the console or with a script while a provider
manages it. A team can also manage one object from OpenTofu and from Pulumi.

| Option | Gives | Costs |
| --- | --- | --- |
| A. Detect at refresh, correct at apply, and fail on a version conflict | The tools' own flow: a plan shows the difference, and an apply with a stale `If-Match` changes nothing (`ErrConflict`). | A difference stays until someone runs a plan. |
| B. A. plus the read returns who last changed the object and when | The plan shows who made the out-of-band change. The server has this in the audit entries. | A small field on the read, or a call to the audit log. |
| C. A reconcile loop in the server or in an agent that corrects the object. | No drift. | The server then writes objects by itself, which breaks "a person or a token is the actor" for audit. It also fights a team that uses the console on purpose. |

**Decided in ADR 270 decision 8: A, with B as a follow-up.** An update and a delete always
send the version that the read returned. A conflict returns `ErrConflict` and
changes nothing. The provider does not retry with a newer version by itself:
the person plans again and sees the difference. For an object that two tools
manage, the second plan shows the difference too, so no extra field is needed
in the first release.

Benchmark: Terraform and Pulumi both refresh in plan. Kubernetes server-side
apply tracks which manager owns each field and answers a conflict instead of
overwriting. AWS CloudFormation drift detection reports a difference and does
not correct it. Google Config Connector corrects drift in a loop by default,
which is option C and which a regulated persona may not want for policy. PCI
DSS 4.0 requirement 11.5.2 asks for a change-detection mechanism for critical
files, and SOC 2 CC7.1 asks that changes to configuration are detected. A plan
that shows the difference meets both for the objects under management.

### 8. What is the format of the version pin?

ADR 270 decision 1 asks for a version pin before 1.0. The issue asks that a
client "refuses a server version outside its pin, with a message that names
both versions". ADR 264 decision 5 says that the CLI and the providers each
pin "the version of the description that it was generated from". That is a pin
on the description, not on the server.

| Option | Gives | Costs |
| --- | --- | --- |
| A. An exact version | The strictest. | Each server patch needs a new client release. |
| B. A range `>=A <B`, with the same minor version before 1.0, and the same major version after | A patch does not break the client. The range is easy to read and the message can name it. | Needs a rule for a pre-release and for a dev build (below). |
| C. A pin on the version of the API description only | It is what the client was generated from, and ADR 264 requires it. | The server version is what an operator knows. A second number to explain. |
| D. A compatibility check at run time (call the operation and see if it fails) | No pin. | A client may do a half change before it fails. It does not meet the issue. |

**Decided in ADR 270 decision 9: C is required, and B on the server version
may be added on top.** Both checks run once, before any other call. Both must
pass.

**The description pin.** The pin has the form `>=MIN <MAX`. A client generated
from description `0.12.3` pins `>=0.12.0 <0.13.0` before 1.0. A pin to the
minor range of the description satisfies ADR 264 decision 5. A description
version outside the pin, or a server that sends none, is refused with a message
that names the description version and the pin. The refusal has no flag that
skips it, because a check that a flag skips is advice. A dev build of the server
embeds a description, so it sends a version, and this check applies to it in
full.

**The server range.** The range has the same form. The server version rules
are the rules of `pkg/store/schema`, so the project has one reading of a
version:

- `MAJOR.MINOR.PATCH`, with an optional leading `v`, is a release. An optional
  pre-release follows `-`.
- A `git describe` suffix (`-3-gabc1234`, `-dirty`) is not a pre-release. A
  describe build counts as its tag, and the client compares that tag.
- `dev` and a commit hash are a dev build. A dev build counts as newer than
  every release. The client does not refuse it. It prints a warning that names
  both versions (for example "server is a dev build; this client expects
  >=0.12.0 <0.13.0") and continues. An empty version is not a dev build: it
  means that the server sent nothing, and the client refuses it.
- Build metadata after `+` is ignored. The client accepts it only when it is
  dot-separated identifiers of `[0-9A-Za-z-]`, none empty, and the whole version
  is at most 64 characters. Any other form is refused, not cut.
- A release outside the range is refused with a message that names the server
  version and the range.

The client follows these rules but differs from `pkg/store/schema` in two
places. First, the empty string is not a dev build in the client: the server
sent no version, and the client refuses it. `pkg/store/schema` treats the empty
string as a dev build. Second, build metadata is allowlisted in the client and
not dropped: `pkg/store/schema` drops the text after `+` without a check, and
the client refuses a form that is not on the allowlist. The client copies the
rules into its own code, because it cannot import `pkg/store/schema`: that
package is in the module of the server, and a client must not pull the
`go.mod` of the server.

**Pre-release.** With a lower bound that is a pre-release, the range accepts a
pre-release of any patch version in the range. This differs from node-semver,
which accepts a pre-release only on the same `MAJOR.MINOR.PATCH` as a bound.
With a lower bound that is a release, no pre-release passes. So `0.12.5-rc.1`
does not pass `>=0.12.0 <0.13.0`, although its number lies between the two
bounds, and `0.13.0-rc.1` does not pass it either. An implementation keeps
this rule.

**The e2e suite.** The suite runs dev server binaries. A dev build passes the
server range by the dev rule (with a warning), and the description pin is the
strict check. The suite therefore has three steps: a client whose description
pin the dev server meets, which works and prints the warning; a client with a
different description pin, which is refused with a message that names both
versions; and a stub server that sends a release outside the server range,
which is refused.

Benchmark: the Kubernetes version skew policy allows `kubectl` one minor
version apart from the API server, and `kubectl` warns about version skew
instead of refusing. Terraform `required_providers` takes a constraint range,
and `required_version` fails with the version it found. Pulumi can pin a plugin
version in the project file. go-tfe reads `TFP-API-Version` and `X-TFE-Version`
from `/api/v2/ping`, which is how a client learns the version. Compare each
before the RFC is accepted.

## The client port

This section is the contract for the client code. It lands in the repository
that question 3 chooses, with a conformance suite. The CLI and the providers
call the port, and an adapter turns each call into a request to the REST API.
The port imports no server package.

### Kinds and references

A reference names one object: its kind, its scope and its ID. The client checks
the reference with an allowlist, refuses a value that is not valid, and does not
change it. It sends no request for an invalid reference (`ErrInvalidRef`).

| Kind | Scope | ID grammar |
| --- | --- | --- |
| `collection` | namespace | The name: 3 to 40 characters of ASCII letters, digits, `-`, `_` and inner spaces. It starts and ends with a letter or a digit. Two names that differ only in case are one name. |
| `approval-rule` | namespace | `[a-z0-9][a-z0-9-]{0,62}`, not `default` and not `owners`. |
| `ownership-rule` | namespace or collection | `[a-z0-9][a-z0-9-]{0,62}`. |

A namespace is 1 to 100 characters of ASCII letters, digits, `-`, `_` and `.`,
and does not start with `.` or `-`. The client encodes each path segment, so an
inner space in a collection name goes to the path as `%20`.

### The import reference

`tofu import`, `pulumi import` and the CLI take one string for an object. The
string joins the namespace, the collection (when the kind has one) and the ID
with `/`. No allowed character of a namespace, a collection name or an ID is
`/`, so the separator needs no escape. The client does not decode `%`, `\` or
any other escape: a value with a character outside its allowlist is refused
(`ErrInvalidRef`), not cut and not changed. The number of segments decides the
form, and a form with a wrong number of segments for its kind is refused.

| Kind | Form | Allowlist of each segment |
| --- | --- | --- |
| `collection` | `NAMESPACE/NAME` | The namespace grammar above, then the collection grammar above. |
| `approval-rule` | `NAMESPACE/ID` | The namespace grammar above, then `[a-z0-9][a-z0-9-]{0,62}`, not `default` and not `owners`. |
| `ownership-rule` in a namespace | `NAMESPACE/ID` | The namespace grammar above, then `[a-z0-9][a-z0-9-]{0,62}`. |
| `ownership-rule` in a collection | `NAMESPACE/COLLECTION/ID` | The namespace grammar, the collection grammar, then `[a-z0-9][a-z0-9-]{0,62}`. |

A collection name may hold inner spaces, so a shell needs quotes around the
reference of a collection and of an ownership rule in a collection. The
reference is a single argument, and the client does not split it at a space.
The namespace may hold `.`, which has no meaning in the reference.

| Kind | Valid example | Refused example |
| --- | --- | --- |
| `collection` | `acme.eu/payments core` | `acme.eu/payments%20core` (`%` is not allowed), and `acme.eu/payments/core` (3 segments). |
| `approval-rule` | `acme.eu/two-person` | `acme.eu/default` (reserved ID), and `acme.eu/Two-Person` (upper case). |
| `ownership-rule` in a namespace | `acme.eu/net-cidr` | `acme.eu/net_cidr` (`_` is not allowed in an ID). |
| `ownership-rule` in a collection | `acme.eu/payments core/net-cidr` | `acme.eu//net-cidr` (empty segment), and `acme.eu/payments core/net-cidr/x` (4 segments). |

An ownership rule in a namespace and an approval rule have the same form, so
the kind of the resource in the code decides how the client reads the
reference. The reference never selects the kind.

### Methods and errors

| Method | Does | Errors |
| --- | --- | --- |
| `ServerVersion` | Returns the server version and the description version. | |
| `Get(ref)` | Returns the object with its `ETag`. | `ErrNotFound` |
| `List(kind, scope, page)` | Returns one page in a stable order, and the cursor of the next page. | `ErrInvalidRef`, `ErrNotFound` (a namespace that does not exist) |
| `Create(ref, spec)` | Makes the object. Sends `If-None-Match: *`. Never adopts. A collection is created with `POST`, which does not read `If-None-Match` today and answers `409` with no code. It needs `If-None-Match: *` with `412`, as the other kinds do (see "Follow-up work"). | `ErrAlreadyExists` (from `412`), `ErrInvalidSpec` |
| `Update(object)` | Replaces or patches the object. Sends `If-Match` with the `ETag` of the read. | `ErrConflict` (from `412`), `ErrNotFound`, `ErrInvalidSpec` |
| `Delete(ref, etag)` | Removes the object. Sends `If-Match`. An object that is already gone is success. Only the approval default answers `204` for a missing object. A missing approval rule or collection answers `404`, so the adapter maps `404` on a delete to success: the object is gone. | `ErrConflict` (from `412`, which stays an error) |

Every method also returns the error of the context, `ErrUnauthenticated`
(`401`), `ErrForbidden` (`403`), and `ErrFreshAuthRequired` (`403` with the code
`mfa.fresh-step-required`). The adapter wraps each error with the detail of the
server and returns no transport error. The `ETag` is opaque. The client stores
it and sends it back as it came.

**Status mapping.** The adapter maps a `409` by its error code, never by its
status alone:

| Answer | The adapter returns |
| --- | --- |
| `412` | `ErrConflict`. |
| `409` with `request.idempotency-key-in-progress` | A retry with the same key (see "Write rules"). |
| `409` with `request.idempotency-key-replayed-secret` | `ErrReplayedSecret`: the replay names the object that the first request made and holds no secret. The client does not retry. It reads the object by its reference. |
| `409` with `gate.final` | `ErrFinal`: the gate has a final decision and the change is refused. |
| `409` with `gate.rule-failed` | `ErrRuleFailed`: a rule refuses the change. |
| `409` with any other code, or with no code | A fail-closed error (`ErrUnexpected`) that holds the status and the code. It is never `ErrConflict`. |

A `409` is not a stale `If-Match`. A client that reads a `409` as `ErrConflict`
sends a stale-version message for a refusal that a new read does not change.

### Write rules

- **`If-Match`.** An update and a delete always carry `If-Match`. A client never
  sends one without a value, and never retries with a newer value by itself. A
  `412` is `ErrConflict` and changes nothing.
- **`Idempotency-Key`.** Every write carries a key of 1 to 255 printable ASCII
  characters, for example a random UUID. A retry after a lost answer sends the
  same key. A `409` with the code `request.idempotency-key-in-progress` means
  the first request has not ended: the client retries later with the same key.
  Every other `409` follows the status mapping in "Methods and errors".
  A `422` with the code `request.idempotency-key-reused` means the same key with
  a different body: the client reports a defect and does not retry. A replay
  answers with the header `Idempotent-Replayed: true` (ADR 210 decision 6).
- **`PUT` or merge patch, by kind.** The client sends the whole spec with `PUT`
  for an approval rule and an ownership rule, because those routes replace the
  object. The approval default is not a first kind (ADR 270 decision 5), so the
  port has no kind for it yet. For a collection, the update route is a JSON
  Merge Patch (RFC 7396) of the fields in the spec, once the route exists. A
  field that the spec leaves out is not changed by a patch, and it is reset by
  a `PUT`. The client states which one a kind uses and tests both cases.

### Two valid specs for each kind

The conformance suite needs two valid specs for each kind, so that an update
test changes something. The first is the base spec, and the second differs in
one field.

| Kind | Base spec | Changed spec |
| --- | --- | --- |
| `collection` | `{"name": "payments"}` | `{"name": "payments", "<field of the update route>": ...}`. The collection update route (follow-up work) decides this field. |
| `approval-rule` | `{"count": 1, "approvers": {"groups": ["netops"], "users": [], "unitOwners": false}, "authorMayApprove": false, "freshAuthSeconds": null, "timeoutSeconds": null}` | the same with `"count": 2` |
| `ownership-rule` | `{"mode": "evaluate", "paths": ["modules/network/**"], "when": "", "groups": ["netops"], "users": [], "count": 1, "freshAuthSeconds": 0}` | the same with `"count": 2` |

The ownership rule fields follow ADR 225 decision 9. The server work for the
ownership rule routes fixes them.

## Proposal

1. Take ADR 270 as the decision of the owner and of the product manager.
2. Build in this order: the server work in "Follow-up work", then the client
   (a hand-written adapter for the three first kinds when the owner approves
   question 2, or the generated layer when RFC 99 has a generator), the CLI,
   the OpenTofu provider and the Pulumi provider.
3. Questions 2 and 3 wait for the owner. For question 3, present the registry
   naming check as a precondition, and the rule that a client release ships on
   the day of each server minor release.
4. The e2e suite drives the real OpenTofu CLI and the real Pulumi CLI to
   create, update and delete a collection, an approval rule and an ownership
   rule, before any provider endpoint is claimed.
5. The user documentation has a page when a person can use the first client
   (ADR 214).

## Threat notes

A client that holds a token is security code. The steps are those of ADR 250.

| Step | Note |
| --- | --- |
| 1. Check the input | The reference check is an allowlist for the namespace and the ID. The version parser reads only the accepted forms and caps the length. The server version, the description version and the error text from the server are untrusted. |
| 2. Encode or contain | The adapter encodes each path segment and sends JSON from typed values. A server version in an error message is quoted and cut to a fixed length. Build metadata with a character outside the allowlist is refused. |
| 3. Limit the effect | An update and a delete always carry `If-Match`, so a stale client changes nothing. A client has a timeout and a size limit for a response. |
| 4. Check authorization | The server checks the action on the object for every call. The client assumes nothing. |
| 5. Fail closed | An unknown kind, a bad reference, a version outside the pin and a server with no version number all refuse. A `403` with `mfa.fresh-step-required` is an error, not a retry with another credential. |

Tokens never go in the state of OpenTofu or Pulumi. The provider reads them
from the environment or from a file, marks them as secret in its schema (ADR
175), and never logs them.

## Follow-up work

Each item is a separate issue with the label `needs-triage`. The first five are
server work and block the first provider release in issue 967.

- #1230: a route that gives the server version and the version of the
  description to any signed-in caller, with no `server:observe` (question 8);
- #1237: a collection update route with `ETag` and `If-Match`;
- #1238: REST routes for ownership rules;
- #1239: create-only writes (`If-None-Match: *`) on `PUT` routes that upsert
  and on the `POST` route that creates a collection (it ignores `If-None-Match`
  today and answers `409` with no code, and it needs `412`), which blocks the
  first provider release, and a `412` for a stale `If-Match` on the approval default and approval
  rule routes of gate configuration, which answer `409` today (ADR 210
  decision 6 says `412`);
- #1240: writes to gate configuration accept an access token for a change that
  does not weaken security, and answer `mfa.fresh-step-required` for one that
  does;
- #1231 and #1233: the client and the CLI, and the providers, after the owner
  answers questions 2 and 3;
- #1232: the e2e steps for the real OpenTofu CLI and the Pulumi CLI;
- #1234: a later RFC for a step-up sign-in of a person in the CLI (question 6,
  option B), which must keep the rule that an access token never gives fresh
  authentication, or go to the owner;
- the user documentation page for the first client (ADR 214).

## Outcome

Empty until the RFC is accepted or dropped.
