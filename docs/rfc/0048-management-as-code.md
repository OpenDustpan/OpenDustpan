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
repository; ownership rules are managed as code through the providers. This
RFC holds the questions that stay open. Each has options, a recommendation and
a benchmark. Question 2 needs the owner, because it asks for a generator
dependency. On acceptance, the answers go into an ADR. The only code is the
client port, the version pin rule and a conformance suite skeleton in
`pkg/manage`.

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
  210). Every mutating route takes an `Idempotency-Key`, and `PATCH`, `PUT` and
  `DELETE` take `If-Match` with the `ETag` of the object (ADR 210).
- The API description (ADR 264, RFC 99) is in design. The package
  `internal/api/apidesc` holds the types and the drift check. No description is
  served yet, and no generator is chosen (RFC 99 question 6).
- `GET /api/dustpan/v1/capabilities` is public and gives `apiVersion`
  (`v1`) only. The server version is in `/api/server/upgrade-check`, which
  needs `server:observe` on the installation. A provider token should not need
  that right, so a client has no route today that gives it the server version
  (see "Follow-up work").
- An access token never gives a fresh authentication (ADR 227 decision 6). A
  write that needs one answers `403` with the error code
  `mfa.fresh-step-required`. A change to a weaker approval setting needs one
  (ADR 225, ADR 212 decision 3), and so does an approval of a `destroy`.
- Provider wrappers exist as design (ADR 170 to 179): the module
  `providerkit` builds one provider that works with OpenTofu and with the
  Pulumi CLI from one core and two thin protocol adapters, in a repository of
  its own. The issue asks for the providers "through providerkit".
- Policy objects exist: an approval rule has approvers, a count and
  `freshAuthSeconds` (ADR 225 decision 1). An ownership rule has a scope, a
  mode (`enforce` or `evaluate`), path globs and a count (ADR 225 decision 9).
  A collection is a named set of units in a namespace (ADR 51).

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

**Recommendation: A.** The client port and the pin rule are tool-neutral, and
the CLI exercises them without a plugin protocol. The providers then add only
their protocol adapters (ADR 176). Every endpoint lands with its e2e step first
(`CLAUDE.md`).

Benchmark: HashiCorp keeps `go-tfe`, the client library, apart from the `tfe`
provider and the `tfe` CLI role, and both use the library. The GitHub CLI and
the Terraform provider for GitHub each use a client library of their own. In
each case the client library comes first and tools are thin layers over it.

### 2. A client written by hand, or generated from the description?

**This question needs the owner.** A generator is a new dependency. The
project asks the owner before it adds a dependency (`CLAUDE.md`), and RFC 99
question 6 holds the trial and the license rules. ADR 264 decision 5 says that
the CLI and the providers are generated from the published description. This
RFC does not choose a generator and does not decide this question.

| Option | Gives | Costs |
| --- | --- | --- |
| A. Hand-written client | No generator dependency. Full control of errors, retries and `If-Match`. | A person writes each call. The client can drift from the server, which is the problem ADR 264 solves. |
| B. Generated client from the description, behind the port | Matches the server by construction. A new route costs no call code. | Needs an approved generator and a published description (RFC 99). A generated client has awkward errors, and the port must wrap it. |
| C. The port in `pkg/manage`, a small hand-written transport (`Idempotency-Key`, `If-Match`, cursors, error translation), and a generated typed layer behind it | The CLI and the providers depend on the port only, so a later change of the generator touches one adapter. This is the RFC 99 shape of the transport layer. | The same dependency as B. |

**Recommendation: C, and do not start the generated layer before the owner
approves a generator.** Until then, the port and its conformance suite are the
contract, and a hand-written adapter for the three first kinds is enough for
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

**Recommendation: B.** One repository, with the CLI and the provider core in
different modules, and one release that builds the CLI, the OpenTofu archive and
the Pulumi archive (ADR 177). Before 1.0, a release of the repository pins one
minor version of the server, so the tags move with the server (question 8).
Check the naming rules of the OpenTofu registry and of the Pulumi plugin
download before the owner chooses the name of the repository. If a registry
needs a repository for each provider, option C replaces B, and the shared
client module stays.

Benchmark: HashiCorp keeps `terraform-provider-tfe` and `go-tfe` in separate
repositories. Pulumi keeps `pulumi-pulumiservice`, the provider for Pulumi
Cloud, in a repository of its own, with its own release cadence, apart from the
Pulumi CLI. The GitHub CLI is one repository apart from the GitHub servers.

### 4. Which objects come first?

Criteria for an object in the first release. The object:

1. is configuration, not state, not a secret and not a runtime record;
2. has a stable ID and the full create, read, update and delete life cycle in
   the REST API, with `ETag` and `If-Match`;
3. is a control that the regulated persona needs under review (a policy);
4. has a conflict rule that a client can show: a change by another actor
   gives `409` or `412`, and the server changes nothing.

| Option | Objects | Note |
| --- | --- | --- |
| A. The three of the issue | Collection, approval rule, ownership rule | They meet 1, 2 and 4. Approval rules and ownership rules meet 3. |
| B. A. plus namespace, gate and access limit | A larger set. | Each needs its own life cycle check, and a wrong gate locks a team. |
| C. Collections only | The smallest set. | It leaves out the policy objects that motivate the work (ADR 270 decision 3). |

**Recommendation: A**, as the issue states. The next group is: namespace,
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

**Recommendation: A in the first release, B later, never C.** The port
returns `ErrAlreadyExists` for a create that finds an object, and its message
names the reference and the one command that helps (the import command of
the tool). The import reads the object and records its version, so the first
plan shows the difference between the code and the server.

Benchmark: Terraform has `import` blocks with `-generate-config-out`, and
Pulumi has `pulumi import` with code generation, so A then B is the path that
both tools take. Google Config Connector acquires an existing resource only
when the manifest asks for it with an annotation, so adoption is explicit. For
a regulated entity, an unreviewed take-over bypasses the change record (DORA
Article 9 names ICT change management; SOC 2 CC8.1 asks that changes are
authorized before they are made).

### 6. How do the CLI and the providers sign in?

An access token is the credential of a script, a pipeline and a provider (ADR
53). The token comes from an environment variable or a file that the tool
reads, never from the code, and never from the state of OpenTofu or Pulumi. A
write that needs a fresh authentication cannot use an access token (ADR 227
decision 6). The approval-rule write that makes a rule weaker is one of those
writes.

| Option | Gives | Costs |
| --- | --- | --- |
| A. The client returns `ErrFreshAuthRequired` and its message names the path of a new sign-in. The write changes nothing. | Matches ADR 227 with no new server code. A provider shows the error in the plan or in the apply. | A pipeline cannot weaken an approval rule. A person must do that step. |
| B. A step-up for a person: the CLI opens a sign-in, the person completes MFA, and the CLI gets a short-lived credential that carries the fresh authentication. | A person at a terminal can apply a rule change in one run. This is how a cloud CLI works. | Needs server code and a decision of the owner, because ADR 227 says an access token never gives a fresh authentication. A new credential type is a new design. |
| C. An exemption for a service principal. | A pipeline can change any rule. | It breaks ADR 227 and the control that the regulated persona needs. |
| D. A change of an approval rule goes through a change order that people approve. | The review that the regulated persona wants. | The change of a policy becomes a long process, and the provider cannot show the result in one apply. |

**Recommendation: A now, B in a later RFC, never C.** Under A, a pipeline
can create and tighten approval rules and ownership rules, and a person must
apply the changes that weaken them, with a fresh authentication. The error
code is `mfa.fresh-step-required`, and the provider shows it as an error of
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
Option C has no benchmark in these standards.

### 7. What happens when the provider state and the server state differ?

Someone can change an object in the console or with a script while a provider
manages it. A team can also manage one object from OpenTofu and from Pulumi.

| Option | Gives | Costs |
| --- | --- | --- |
| A. Detect at refresh, correct at apply, and fail on a version conflict | The tools' own flow: a plan shows the difference, and an apply with a stale `If-Match` changes nothing (`ErrConflict`). | A difference stays until someone runs a plan. |
| B. A. plus the read returns who last changed the object and when | The plan shows who made the out-of-band change. The server has this in the audit entries. | A small field on the read, or a call to the audit log. |
| C. A reconcile loop in the server or in an agent that corrects the object. | No drift. | The server then writes objects by itself, which breaks "a person or a token is the actor" for audit. It also fights a team that uses the console on purpose. |

**Recommendation: A, with B as a follow-up.** An update and a delete always
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
both versions". The code in `pkg/manage` implements a rule with the form that
is the recommendation below, so the rule is testable now. The format is not
decided.

| Option | Gives | Costs |
| --- | --- | --- |
| A. An exact version | The strictest. | Each server patch needs a new client release. |
| B. A range `>=A <B`, with the same minor version before 1.0, and the same major version after | A patch of the server does not break the client. The range is easy to read and the message can name it. | Needs a rule for a pre-release (below). |
| C. A pin on the version of the API description, not on the server | The description is what the client was generated from (ADR 264 decision 5). | The server version is what an operator knows. A second number to explain. |
| D. A compatibility check at run time (call the operation and see if it fails) | No pin. | A client may do a half change before it fails. It does not meet the issue. |

**Recommendation: B, on the server version.** The pin is `>=0.12.0 <0.13.0`
before 1.0. The generated code carries it. The rules, which `pkg/manage` holds
as code:

- A server version below the lower bound or at or above the upper bound is
  refused with a message that names the server version and the pin.
- A server version that is not a version number (`dev`, empty) is refused.
- A pre-release of the server is accepted only when the lower bound is a
  pre-release too, so `0.13.0-rc.1` does not pass `>=0.12.0 <0.13.0`.
- There is no flag that skips the check, because a check that a flag skips is
  advice. A team that must use a newer server than its client waits for the
  client, or pins an older client.
- The client checks the pin once at the start, before any other call.

The client needs the server version without a right that a provider token
does not have (see the facts and the follow-up work). The version of the
description (option C) can be an extra check later.

Benchmark: the Kubernetes version skew policy allows `kubectl` one minor
version apart from the API server, which is a pin by minor. Terraform
`required_providers` takes a constraint range, and the provider
`required_version` fails with the version it found. Pulumi can pin a plugin
version in the project file. All three refuse a version outside a range and
name it.

## Proposal

1. Take ADR 270 as the decision of the owner.
2. Build in this order: the client port and the pin rule (this change), a
   hand-written adapter for the three first kinds when the owner approves
   question 2 (or the generated layer when RFC 99 has a generator), the CLI,
   the OpenTofu provider and the Pulumi provider.
3. Questions 1, 3 to 8: take the recommendations above, unless the owner
   changes one. Question 2 waits for the owner.
4. The e2e suite drives the real OpenTofu CLI and the real Pulumi CLI to
   create, update and delete a collection, an approval rule and an ownership
   rule, before any provider endpoint is claimed.
5. The user documentation has a page when a person can use the first client
   (ADR 214).

## Threat notes

A client that holds a token is security code. The steps are those of ADR 250.

| Step | Note |
| --- | --- |
| 1. Check the input | `Ref.Validate` is an allowlist for the namespace and the ID. `ParseVersion` and `ParsePin` read only the accepted forms and cap the length. The server version and the error text from the server are untrusted. |
| 2. Encode or contain | The adapter encodes each path segment and sends JSON from typed values. A server version in an error message is quoted and cut to a fixed length. |
| 3. Limit the effect | An update and a delete always carry `If-Match`, so a stale client changes nothing. A client has a timeout and a size limit for a response. |
| 4. Check authorization | The server checks the action on the object for every call. The client assumes nothing. |
| 5. Fail closed | An unknown kind, a bad reference, a version outside the pin and a server with no version number all refuse. A `403` with `mfa.fresh-step-required` is an error, not a retry with another credential. |

Tokens never go in the state of OpenTofu or Pulumi. The provider reads them
from the environment or from a file, marks them as secret in its schema (ADR
175), and never logs them.

## Follow-up work

Each item is a separate issue with the label `needs-triage`:

- a route that gives the server version and the version of the description to
  any signed-in caller, with no `server:observe` (needed by question 8);
- the client adapter and the CLI, after the owner answers question 2;
- the e2e steps for the real OpenTofu CLI and the Pulumi CLI;
- the provider for OpenTofu and the provider for Pulumi, on `providerkit`;
- a later RFC for a step-up sign-in of a person in the CLI (question 6,
  option B);
- the user documentation page for the first client (ADR 214).

## Outcome

Empty until the RFC is accepted or dropped.
