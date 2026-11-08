---
rfc: 123
title: "Pluggable sweepers: a protocol, an SDK and a repository for sweep plugins"
status: discussing
created: 2026-11-07
---

# RFC 123. Pluggable sweepers: a protocol, an SDK and a repository for sweep plugins

Status: discussing. Design only. Nothing in this RFC is decided, and no code
exists for it. The owner answers the questions at the end (ADR 15). Until the
owner decides, ADR 164 and ADR 343 stay as they are.

## Summary

A cloud sweep (ADR 164) runs a command on a runner of the team. The command
lists the resources of an account and writes a sweep report. Today the team
must find or write that command. On 7 November 2026 the owner asked for
converters that live outside the application repository, that are easy to
contribute, and that are pluggable. The owner also asked whether a small SDK
lets a provider of OpenTofu or of the Pulumi CLI add sweep support natively,
so that sweeps can later cover the common providers of both registries.

This RFC tests the product manager's sketch against primary sources, and
proposes:

1. **A sweep plugin** is a separate, signed program with a small, versioned
   contract: a `describe` call that returns a manifest, and a `sweep` call
   that writes a sweep report of version 2. The contract is a process
   contract (arguments, a file and an exit code), not a gRPC service.
   Section "Option A" says why.
2. **A new repository**, `OpenDustpan/sweep-plugins`, holds the contract, a
   Go SDK, the conformance suite `sweepplugintest` that runs against
   recorded API responses, the first-party sweep plugins, and the community
   sweep plugins that the maintainers accept. The server links no sweep
   plugin code. The repository needs its own ADR, because ADR 343 decision 6
   gives no other component a repository.
3. **On the runner**, the launcher fetches a sweep plugin from the server by
   digest, checks that the bytes are the admitted bytes, and runs it in the
   sandbox of the pool. Whether a release may be admitted is the
   administrator's policy over provenance facts (ADR 351). The job proxy
   holds a short-lived read-only credential whose token binds the target
   and the plugin. What else the host can reach, for example a write
   identity, is the operator's responsibility (owner, 2026-11-07). The server treats the report as
   unverified facts, as ADR 164 decision 17 says, and shows coverage for each
   resource type: swept, not swept or not supported.
4. **A generic bridge to OpenTofu and Terraform providers**, which starts a
   provider binary and calls the `ListResource` call of the provider plugin
   protocol (versions 5.10 and 6.10), with resource identity in the record.
   It needs no CLI: OpenTofu does not use the call, but the providers have
   it. AWS, AzureRM and Google have list resources today. It uses only the
   MPL-2.0 protocol files of OpenTofu.
5. **For Pulumi, no convention of our own.** The Pulumi provider protocol has
   a native, streamed `List` call since Pulumi 3.233.0, and the Terraform
   bridge of Pulumi maps `ListResource` to it. A second generic bridge calls
   it. The provider kit (ADR 170) implements both calls later, so that a
   provider built with it is swept with no plugin of its own.
6. **First-party sweep plugins** for AWS Resource Explorer, Azure Resource
   Graph, Google Cloud Asset Inventory and Kubernetes discovery.
7. **Spend on the same plugins.** By the owner decision of 7 November
   2026, FinOps (RFCs 45 and 67) joins the same milestone, renamed "Cloud
   inventory: sweeps and spend". A plugin can implement the kind `spend` and
   write a spend report, which the server matches against the same resource
   keys. Section "Spend and cost on the same plugins" maps the two RFCs; a
   follow-up RFC designs the FinOps feature.

## Problem

- ADR 164 decision 9 says that OpenDustpan ships no sweeper program in the
  server, and leaves converters open (open question 1). ADR 343 decision 5
  answers only where a converter runs: on a pool that has the sweep purpose.
  It does not decide who writes or ships converters.
- A team that wants the kind "not in any unit" (ADR 163) today needs a
  program that reads its cloud and writes the report format of ADR 164
  decision 10. No such program exists. So no team can use the kind with no
  code of its own.
- The report of version 1 carries the cloud's own type name, for example
  `AWS::S3::Bucket`. It does not carry the IaC type, for example
  `aws_s3_bucket` for OpenTofu or `aws:s3/bucket:Bucket` for the Pulumi
  CLI. So a finding cannot say "import this as `aws_s3_bucket`", and the
  console cannot show which IaC types a sweep covered.
- `unmanaged.KeysOf` has key rules for `aws`, `azure` and `gcp` only. A
  resource of any other provider has no key, so a sweep can never match it.
  The owner's aim, "the most common providers in the registries", needs a
  key rule that does not depend on one cloud.
- A sweep that does not list a type says nothing about that type. Today the
  report has no way to say "I did not look at this type". The console could
  read "no finding" as "nothing unmanaged".
- ADR 343 lets a pool have more than one purpose. ADR 164 decision 5 gave
  the reason for a pool with one purpose: the identity of the host is the
  credential of the sweep. On a pool that also applies changes, that
  identity can write. ADR 343 says that it does not decide how a sweep stays
  read-only on such a pool. This RFC must answer that, because a sweep
  plugin is third-party code that runs with that credential.

## What is decided already

| Source | Rule | What it means here |
| --- | --- | --- |
| ADR 164 decision 1, ADR 343 decision 3 | A sweep runs on a runner of the team, never in the server. | A sweep plugin runs on a runner. The server never starts one. |
| ADR 164 decision 4 | The server holds no cloud credential for a sweep. The only credential is the workload identity of the runner hosts. A target names no profile. | Option D changes this, or it stays. |
| ADR 164 decision 5, ADR 343 decision 4 | A sweep pool ran only sweeps. Now a pool may have more than one purpose. | A sweep on a pool with the purpose `jobs` too must not get the write identity of the host. |
| ADR 164 decisions 10 to 13, 17 | The report format, its bounds, the scope rules, and what a runner that lies can do. | Version 2 keeps every rule of version 1. |
| ADR 343 decision 5 | Converters run on a pool with the sweep purpose. | A sweep plugin is such a converter. |
| ADR 343 decision 6 | Only the runner gets a repository from the exception of ADR 282. | A new repository needs its own ADR. |
| ADR 170, ADR 176, ADR 316 | The provider kit is in `OpenDustpan/providerkit`. It speaks the OpenTofu plugin protocol and the Pulumi provider protocol. The MPL-2.0 `.proto` files of OpenTofu and `go-plugin` are approved for the provider kit only, not for the server. | A sweep SDK in a provider built with the provider kit is natural. The server stays free of the protocol code. |
| ADR 130 decision 2, ADR 137 | The sandboxes: `firecracker`, `gvisor` and `process`. The pool sets egress. The default egress is the network of the host. | A sweep plugin runs in one of these sandboxes. |
| ADR 327, ADR 330, RFC 16 | The job proxy: one process next to each job, the only exit of the sandbox, proxy rules as plain data, and credentials that only the proxy holds (way A ends TLS and adds the credential). RFC 16 puts AWS Signature Version 4 out of scope. | A sweep plugin runs behind the job proxy. Option D must treat AWS apart. |
| RFC 93, ADR 269, ADR 349 | The vetted supply chain; private providers are uploaded as the archive set (zip files, `SHA256SUMS`, its signature); the OpenPGP library is allowed in the server registry. | A sweep plugin release can reuse the archive set and its check. |
| ADR 204 | OpenDustpan is an OIDC issuer for profiles. Whether a sweep gets a token stays open. | Option D3. |
| ADR 351 (PR #2587, open) | OpenDustpan computes provenance signals as facts, and the administrator writes the policy over them. Integrity (the admitted bytes, hashes, size limits) is code. Decision 6 names the sweep plugins of this RFC. | The signature of a plugin is a fact, not a rule (Option C). |
| ADR 250 | Defense in depth for every input from outside the process. | Section "Security". |
| ADR 347 | Warn, do not block, for choices that the operator owns. Security controls keep their own rules. | The signature check and the sandbox are security controls. |


ADR 351 is in pull request #2587, open too. ADR 343 and ADR 347 are in pull request #2562, which is open on the day of
this RFC. This RFC reads them as the owner's decisions of 7 November 2026. If
their text changes before the merge, this RFC changes with them.

## Facts from primary sources

Checked on 7 November 2026, from the source files, the change logs and the
official documentation. Each fact names its source. "Not found" means that the
search found nothing. It does not mean that the thing does not exist.

### The provider plugin protocol of OpenTofu and Terraform

- **`ListResource` was added in protocol versions 5.10 and 6.10.** The
  current version in hashicorp/terraform is 6.11; its changes are not about
  lists. It is a server stream:
  `rpc ListResource(ListResource.Request) returns (stream ListResource.Event)`.
  The request has `type_name`, `config`, `include_resource_object` and
  `limit`. Each event has `identity`, `display_name`, an optional
  `resource_object` and the repeated field `diagnostic`. `GetProviderSchema` returns
  `list_resource_schemas`. `ValidateListResourceConfig` checks the
  configuration of a list.
  Source: `docs/plugin-protocol/tfplugin6.proto` in hashicorp/terraform;
  `docs/plugin-protocol/tfplugin6.10.proto` in opentofu/opentofu.
- **Resource identity exists in protocol versions 5.9 and 6.9.**
  `GetResourceIdentitySchemas` returns, for each resource type, a versioned
  list of identity attributes with `required_for_import` and
  `optional_for_import`. Each `ListResource` event carries such an identity.
  Source: the same files.
- **The licenses of the protocol files.** The `.proto` files carry
  `SPDX-License-Identifier: MPL-2.0`, in hashicorp/terraform (whose root
  license is BUSL-1.1) and in opentofu/opentofu (MPL-2.0).
  `terraform-plugin-go`, `terraform-plugin-framework` and `go-plugin` are
  MPL-2.0. This RFC uses only the copy of OpenTofu, by the rule of the task:
  no code from a repository whose license is BUSL.
- **Terraform** reads list resources from version 1.14.0 (19 November 2025),
  in `*.tfquery.hcl` files and the `terraform query` command. Import by
  identity came in 1.12.0. Source: `CHANGELOG.md` of hashicorp/terraform at
  `v1.14.0` and `v1.12.0`.
- **OpenTofu supports resource identity, and does not use `ListResource`.**
  - Identity: OpenTofu 1.12.0 lets an `import` block take `identity`, and
    the state file of 1.12 stores the identity of a resource. Source:
    `CHANGELOG.md` of opentofu/opentofu, branch `v1.12`;
    `internal/states/statefile/version4.go`.
  - List: `main` (1.14.0-dev) carries protocol 6.10, so its generated code
    has the call. `internal/plugin6/grpc_provider.go` has no `ListResource`
    method, and OpenTofu has no `list` block and no query command. No change
    log entry and no RFC in its `rfc/` directory names it. Whether an issue
    plans it: not found.
  - So a sweep plugin cannot ask OpenTofu to list. It can call the provider
    itself, because the call is in the provider, not in the CLI.
- **The SDKs for provider authors.** `terraform-plugin-go` 0.29.0
  (17 September 2025) has the types; at v0.31.0 the call is still behind the
  temporary interface `ProviderServerWithListResource`.
  `terraform-plugin-framework` 1.16.0 (17 September 2025) adds the `list`
  package and the `list.ListResource` interface. Source: the change logs of
  both repositories.
- **A list needs a configured provider.** The framework passes
  `ListResourceData` from the answer of `ConfigureProvider` to the list
  resources. So a host must call `ConfigureProvider` with real credentials
  before `ListResource`. Source: `terraform-plugin-framework` change log,
  1.16.0.
- **A host needs no Terraform and no OpenTofu.** A host starts the provider
  with the magic cookie variable and `PLUGIN_PROTOCOL_VERSIONS`, gives a
  client certificate in `PLUGIN_CLIENT_CERT` for automatic mTLS, reads the
  handshake line from the standard output and dials gRPC. Source:
  `server.go`, `client.go` and `docs/guide-plugin-write-non-go.md` of
  hashicorp/go-plugin. ADR 176 decision 6 already implements the server side
  of this handshake without the `go-plugin` library. Nobody ran a test of a
  host against a real provider for this RFC.

**Providers with list resources** (count of the pages in the
`list-resources` documentation folder of each provider, at its `main` branch
on 6 November 2026; a rough count):

| Provider | License | First version with list resources | List resource types |
| --- | --- | --- | --- |
| hashicorp/aws | MPL-2.0 | 6.14.0 (18 September 2025) | about 276 |
| hashicorp/azurerm | MPL-2.0 | 4.45.0 (18 September 2025) | about 127 |
| hashicorp/google | MPL-2.0 | 7.29.0 (21 April 2026) | about 160 |
| Azure/azapi | MPL-2.0 | 2.8.0 | 1, generic over every Azure type |
| hashicorp/kubernetes, helm, random, tls, azuread | MPL-2.0 | none | none (Kubernetes has identity only) |

The AWS provider has more than 1,500 resource types (the count of
`website/docs/r` was not checked for this RFC), so about 276 list types is a
part of it, not all of it.

### The Pulumi provider protocol

- **The protocol has a `List` call.** `rpc List(ListRequest) returns (stream
  ListResponse)` is in `proto/pulumi/provider.proto` of pulumi/pulumi
  (checked at `v3.274.1`). The request has `token` (the resource type),
  `query` (a filter that the provider defines), `limit`, `page_size` and
  `continuation_token`. The stream gives `Result{id, name}` items and an
  optional continuation. The file says that `id` "should be an importable ID
  that can be passed to Read". The Pulumi change log puts the protocol call
  in 3.233.0 (30 April 2026), and `List` on the Go `plugin.Provider`
  interface in 3.243.0 (22 May 2026). A bridge in Go and the provider kit
  need 3.243.0 or later. A resource that supports it has `listInputs` in the
  package schema.
- **The protocol has no identity.** A result is an ID and a name. It has no
  identity attributes, no tags and no region.
- **The CLI.** `pulumi do <pkg> <resource> list` uses the call. The command is
  marked experimental. Source: `pkg/cmd/pulumi/do/` at `v3.274.1`.
- **The bridge.** `pulumi-terraform-bridge` maps the Terraform
  `ListResource` to the Pulumi `List` for providers on the plugin framework,
  from v3.130.0 (`pkg/pf/tfbridge/provider_list.go`). For providers on the
  older SDKv2 it answers "not implemented" (`pkg/tfbridge/provider.go`). When
  a Terraform object has no `id`, the bridge makes one from the identity.
  Its own design notes say that this ID is not always a valid import ID
  (`docs/specs/terraform-list-*.md`).
- **Released providers declare it.** `pulumi-aws` v7.54.0 has 273 resources
  with `listInputs`, among them `aws:s3/bucket:Bucket`.
  `pulumi-kubernetes` at `master` has 184. Source: the `schema.json` of each.
- **The type map is there at run time.** The `GetMapping` call of a bridged
  provider answers the key `terraform` (and `tf`) with the provider info,
  which maps `aws_s3_bucket` to `aws:s3/bucket:Bucket`. Source:
  `pkg/tfbridge/provider.go`.
- **Invoke cannot stream.** `Invoke` is unary. `StreamInvoke` was removed in
  3.161.0. Source: `provider.proto` and the change log.
- **Pulumi Cloud discovery is a closed service.** Its documentation says that
  a scan uses Pulumi providers and runs on Pulumi runners or on a runner of
  the customer. No source code or license for the scanner was found.
- **Licenses.** pulumi/pulumi and pulumi-terraform-bridge are Apache-2.0.

So point 5 of the product manager's sketch is answered: Pulumi already has a
native list call. A convention of our own would copy it.

### Prior art for plugins, and what we may use

| Project | License | Protocol | Verdict for OpenDustpan (Apache-2.0, no GPL, AGPL or BUSL) |
| --- | --- | --- | --- |
| hashicorp/go-plugin | MPL-2.0 | a child process; a handshake line on standard output; gRPC over a socket; automatic mTLS | May link, with an approval (ADR 316 approves only its `.proto` files, for the provider kit). ADR 176 shows that the handshake needs no library. |
| Steampipe CLI, Powerpipe, Flowpipe, Tailpipe | AGPL-3.0 | - | May not use. We do not link it and do not ship it. |
| steampipe-plugin-sdk and the plugins of Turbot | Apache-2.0 | go-plugin, gRPC | May link. Its protocol serves the database host of Steampipe, and is not a public contract. Distribution is OCI with no signature check in the documentation. |
| CloudQuery CLI, plugin-sdk, plugin-pb-go | MPL-2.0 | gRPC over a socket, Apache Arrow records | May link the SDK. The CLI downloads need a sign-in, and the CLI sends telemetry. |
| CloudQuery AWS, Google Cloud and Azure plugins | paid, by a post of the vendor dated 22 April 2024; the source left the repository in commit 19b6912 of 24 April 2024; no license stated on the hub. Not verified by a second source: the review found no date in the repository. | - | May not use. A paid plugin with no stated license is impossible for us. |
| Terraformer | Apache-2.0 | cloud SDK lists, then the Terraform provider for each read | May not use as a dependency: the repository is archived since 16 March 2026 and pins Terraform 0.12. Its method is prior art. |
| former2 | MIT | browser JavaScript with the AWS SDK | Prior art only. No releases. |
| Terraform core 1.6 and later | BUSL-1.1 | - | May not use. |
| Terraform and OpenTofu providers | mostly MPL-2.0; check each | the provider protocol | May run as an external process. We do not link a provider. A provider whose license forbids this use is impossible. |
| Pulumi providers and the bridge | Apache-2.0 | the Pulumi provider protocol | May run as an external process. |

Facts on distribution: the registry protocol of OpenTofu signs a provider
release with a `SHA256SUMS` file and its OpenPGP signature. OpenTofu skips
the check when the registry has no key, unless
`OPENTOFU_ENFORCE_GPG_VALIDATION=true`. Steampipe documents no signature
check. CloudQuery checks only a SHA-256 from its hub, and continues with a
warning when the hub sends none. Source: the documentation pages of each. In
OpenDustpan the signature result is a provenance fact that the
administrator's policy reads; the check that the bytes are the admitted
bytes is fixed in code (Option C, ADR 351).

The magic cookie of `go-plugin` "is not a security measure" (`server.go`).
`SecureConfig` of `go-plugin` hashes the file by path and runs it later, so a
check and a use are apart in time. A sweep plugin must not depend on either.

### The cloud inventory services

| Service | Read-only permission | Page size | Setup | Coverage |
| --- | --- | --- | --- | --- |
| AWS Resource Explorer | `AWSResourceExplorerReadOnlyAccess` (`resource-explorer-2:Search`) | `ListResources` 1 to 1,000 per page. `Search` returns only the first 1,000 results in all. | A local index in each region, and an aggregator index for a search of all regions. A view. | the page of supported resource types |
| Azure Resource Graph | `read` on the resources (`Reader`) | 1,000 rows per page; 30 seconds timeout; throttled per user | none | the page of supported tables |
| Google Cloud Asset Inventory | `roles/cloudasset.viewer` and `roles/serviceusage.serviceUsageConsumer` | `searchAllResources` 500; `assets.list` 1,000 | the API enabled | the page of supported asset types |
| Kubernetes discovery | `system:discovery`, and `list` on each kind | `limit` and `continue`; a token ends after about 5 minutes (`410 Gone`) | none | every served kind, CRDs included |

Facts that change the design:

- The AWS `Search` call is not a full list. A sweep plugin must use
  `ListResources`. When an index or a view is missing in a region, that
  region is not complete.
- Azure Resource Graph leaves out, with no error, what the caller cannot
  read. So a plugin cannot prove that a scope is complete. The coverage of
  a type must say "swept with the permissions given", and the guide must say
  that `Reader` on the whole subscription is needed.
- The Kubernetes `view` role leaves out Secrets, Roles and RoleBindings. A
  plugin with that role must mark those kinds as not swept, not as empty.

## The product manager's sketch, tested

| Point | Verdict | Why |
| --- | --- | --- |
| 1. A separate program behind a small, versioned protocol, gRPC over stdio, with Describe and a streamed List | Keep the two calls and the separate program. Change the transport. | `go-plugin` does not speak gRPC over standard input and output: it prints one line and serves gRPC on a socket. A sweep is one batch: list, then end. It has no calls back and forth. A process contract (arguments in, a report file out, an exit code) carries the same two calls, has no socket in the sandbox, needs no mTLS, and any language can implement it. The plugin can still use gRPC inside, to talk to a provider. Option A. |
| 1. Each item has an identity, account, region, tags, an attribute hash and the IaC type map | Keep, with two changes. | The identity is not the same thing in each tool: OpenTofu has identity attributes, Pulumi has an import ID only. So a record carries one entry for each tool. The attribute hash has no consumer in ADRs 163 to 165 today. It is optional in version 2 and the server does not read it until a feature needs it. |
| 2. A new repository with the protocol, a Go SDK, a conformance suite on recorded API responses, community plugins as signed artifacts; the server links no plugin code; an ADR | Keep. | ADR 343 decision 6 makes the ADR necessary. Option B compares it with the provider kit repository. |
| 3. Fetch by digest, check the signature, a sandbox, read-only credentials from a profile, the job proxy; the report is unverified; coverage per type | Keep, with a stronger credential rule. | A long-lived cloud key in a profile, released to the plugin, is weaker than ADR 164 decision 4 today. Option D proposes a short-lived token from the OIDC issuer of ADR 204 instead, through a profile login block. AWS Signature Version 4 cannot go through the job proxy (RFC 16), so for AWS the plugin sees a short-lived credential. That is a gap, and the RFC states it. |
| 4. A generic bridge to OpenTofu and Terraform providers through `ListResource` and identity, with no CLI, with the MPL copy of the protocol | Keep. | The call is in the provider, so the bridge does not need OpenTofu to support lists. AWS, AzureRM and Google have list resources today. Kubernetes has none, so the Kubernetes plugin stays a first-party plugin. |
| 5. Pulumi: a list hook, or a convention of our own | Drop the convention. Use the native `List`. | `List` is in the Pulumi protocol since 3.233.0, and the bridge maps the Terraform call to it. A generic bridge to Pulumi providers calls it. It gives an ID only, so a Pulumi-only plugin matches by import ID. |
| 6. First-party plugins: AWS Resource Explorer, Azure Resource Graph, Google Cloud Asset Inventory, Kubernetes discovery | Keep. | They cover the most types with one read-only role each. The table above gives the setup that each needs. |

## Options

### Option A. The contract between the launcher and a sweep plugin

- **A1. gRPC over a socket, with the `go-plugin` handshake.** The launcher
  starts the plugin, reads a handshake line, dials gRPC and calls
  `Describe` and `List`.
  - Gives: typed messages, a stream, a cancel through the context.
  - Costs: a socket and mTLS inside the sandbox; gRPC and protobuf in the
    runner module (a new dependency there, ADR 139); a plugin in a language
    with no gRPC library is hard; two state machines (the plugin and the
    launcher) for one batch.
- **A2. A process contract (recommended).** The plugin is a program with
  two commands:
  - `describe` writes the manifest as one JSON document to the standard
    output and ends. It reads no network.
  - `sweep --request <file> --report <file>` reads a request (the account
    scopes, the regions, the types to sweep and the configuration of the
    target), writes a sweep report of version 2 to the report file, and
    ends with an exit code from a fixed table.
  - The launcher already starts a command with an argument list and reads a
    JSON Lines report (ADR 134, ADR 164). This adds a fixed shape to that
    command.
  - **Versions and negotiation.** The manifest has `manifestVersion`, the
    list `contractVersions` and the list `reportVersions`. The launcher calls
    `describe --contract <versions>` with the contract versions that it
    runs. The plugin answers with a manifest of one version that it shares
    with the launcher, or exits with the code "no shared version". The
    parser refuses an unknown field, so a new field comes only with a new
    manifest version, and the launcher reads every manifest version that it
    lists.
  - Gives: no new transport, no socket, no dependency in the runner; any
    language; a test is a file in and a file out.
  - Costs: no typed messages; the shape lives in a JSON Schema and in the
    conformance suite.
- **A3. Do nothing.** The command of ADR 164 stays free. A team writes its
  own. The owner asked for more, so this does not answer the request.

The stream of A1 does not give a lower memory use than A2: the plugin writes
each record as it reads it, and the launcher uploads the file as a stream
(ADR 164 decision 12).

### Option B. Where the code lives

- **B1. A new repository, `OpenDustpan/sweep-plugins` (recommended).** It
  holds the contract (`docs/`), the Go SDK, the conformance suite
  `sweepplugintest`, the first-party plugins, the two bridges, and the
  community plugins that the maintainers accept. It copies the gates of this
  repository, as ADR 170 decision 4 asks for the provider kit. It imports no
  package of OpenDustpan, and OpenDustpan imports none of it.
  - Gives: a plugin is built, tested and released apart from the server. A
    contributor needs no server to add one. Its module graph has the cloud
    SDKs; the server's has none.
  - Costs: a new repository needs an ADR (ADR 343 decision 6); the report
    format has two copies, a parser in the server and a writer in the SDK,
    so a fixture set must keep them equal.
- **B2. In the provider kit repository.** The provider kit already speaks
  both provider protocols (ADR 176).
  - Gives: no new repository.
  - Costs: the provider kit is for providers that a tool starts; a sweep
    plugin is a program that the launcher starts. The cloud SDKs of four
    first-party plugins would enter the module graph of every provider
    author. It mixes two release lines.
- **B3. A separate Go module in this repository**, as the runner module was
  first (ADR 139).
  - Gives: one repository, one CI.
  - Costs: the owner asked for converters outside the application
    repository, so this does not answer the request.
- **B4. No repository: a list of community programs.** OpenDustpan
  documents the contract, and plugins live anywhere.
  - Costs: no first-party plugins, no review and no signature by the project.

With B1, the provider kit still matters: a provider built with the provider
kit should implement `ListResource` and the Pulumi `List` in its two protocol
adapters. Then the generic bridges sweep it with no plugin of its own. That is
the "SDK that a provider adopts natively" of the owner's text, and it is the
protocols of the tools, not a new interface. It is a later change to ADRs 172
and 176, not part of this RFC.

### Option C. Distribution and trust

- **C1. The archive set: fixed integrity, and provenance as facts with a
  policy that the administrator writes (recommended).** This follows ADR 351
  (in PR #2587, open on the day of this RFC), whose decision 6 names the
  sweep plugins of this RFC. A sweep plugin release is the archive set of
  ADR 269: one zip for each operating system and architecture, `SHA256SUMS`,
  and its OpenPGP signature `SHA256SUMS.sig`. The zip holds the binary and
  the manifest.
  - **Integrity is code, and no administrator can turn it off** (ADR 351
    decision 5):
    - **The archive.** At most 256 MiB compressed and 1 GiB unpacked, at
      most 64 files, regular files only: no symbolic link, no hard link, no
      device, no absolute path, no `..` element, no name twice. The server
      and the launcher each refuse an archive that breaks a rule, and do not
      repair it.
    - **Only the admitted bytes.** The server stores the archives of an
      admitted release by digest and serves only those bytes. The claim
      names the digest. The launcher checks the SHA-256 of what it got
      against the claim and against `SHA256SUMS`, with no trust in the
      server's check, and refuses any difference.
    - **Revocation.** A revoke of a release, as ADR 269 decision 11. The
      server refuses a revoked release at a change of a target and when it
      makes a claim, and the claim carries the revocation list. The launcher
      also reads a revocation list from the coordinator configuration, which
      the server does not serve, so a compromised server that holds back its
      list does not stop a revoke that the operator of the runner made. An
      audit entry records each revoke.
    - **The start.** The launcher starts the checked binary from a file
      descriptor that it opened for the check (`execveat` with
      `AT_EMPTY_PATH`), not by a path. For `firecracker`, the launcher runs
      inside the microVM, so it fetches and checks the archive inside the
      microVM. The launcher runs only on Linux (ADR 130), so no other system
      is in scope.
  - **Provenance is facts, and the administrator writes the policy**
    (ADR 351 decisions 1 to 4):
    - At admission the server computes the signals as facts: the signature
      result (`verified`, `unsigned`, `unknown-key` or `failed`, with the
      code of `pkg/registry/pgpverify`, ADR 349), the key ID and its
      fingerprint, whether the key changed since an earlier release of the
      same name, the publisher, the plugin name, the manifest (kinds, types,
      egress hosts, read permissions), and the age of the release.
    - A gate over those facts (ADR 222 to ADR 230), at the installation or
      the namespace, decides the admission. Each allow and each deny names
      the policy and the facts that it read.
    - **Example policies** that OpenDustpan ships as data to copy and edit,
      not as defaults: "only `verified` signatures"; "the project key only
      for names under `opendustpan/`, and a key of the team only for names
      under `acme/`" (a key bound to names, so a team key cannot sign
      `opendustpan/aws-resource-explorer`); "a person approves each new
      key"; and, for a regulated organization, a record of each approved
      release.
    - **Plugin names and patterns.** A plugin name has two segments,
      `<publisher>/<name>`, each of lower-case letters, digits and `-`, at
      most 64 bytes each. A pattern is a full name, or `<publisher>/*`,
      where `*` matches one segment and never crosses `/`. A person with the
      new action `server:manage-sweep-plugin-keys` adds a trusted key and its
      patterns to the installation; an audit entry records the key
      fingerprint and the patterns. The admission gate reads the key and
      whether the name matches its patterns as facts.
    - **The runner may hold its own policy.** The coordinator configuration
      may pin keys, each bound to plugin name patterns of the same form. When it does, the launcher
      also checks the signature against those keys before the start. This
      check is a choice of the operator of the runner, and it is the only
      signature check that holds when the server is compromised. The guide
      recommends it.
  - **Where the policy belongs.** ADR 269 decision 9 keeps keys for each
    namespace, because a namespace uploads its own providers. A sweep plugin
    gets a cloud credential, so this RFC proposes that an installation
    administrator admits releases, and that a namespace chooses only among
    admitted releases. ADR 351 lets the gate sit at the installation or the
    namespace, so the follow-up of RFC 93 may change this.
  - **The project key.** The release workflow of `sweep-plugins` signs only
    on a protected tag, through a GitHub environment with required
    reviewers. The key stays in a hardware or cloud key service that only
    that environment can call. No workflow of a pull request can reach it.
    The new-repository ADR decides the details.
  - Costs: a runner that pins keys needs an OpenPGP library in the launcher,
    a dependency that needs the owner's approval for the runner repository
    (ADR 316 approves it only for the provider kit, ADR 349 for the server
    registry).
- **C2. Sigstore keyless signatures and SLSA provenance.** Stronger
  provenance, but a new trust root, a transparency log on the public
  internet (an installation with no internet cannot check it online) and a
  larger dependency. A later addition, not the first.
- **C3. The OCI image of the team.** The plugin is in the image of the
  sandbox, as the command of ADR 164 is today. No signature check by
  OpenDustpan. This stays for a custom command (see "Proposal"), not for an
  admitted plugin.

The check of the admitted bytes is fixed in code. Whether a signature, a key
or a publisher is enough is the administrator's policy, by ADR 351.

### Option D. Credentials, now that a pool may have more than one purpose

- **D1. The workload identity of the host** (ADR 164 decision 4, today).
  Read-only only when the host role is read-only. On a pool that also has
  the purpose `jobs`, the host role can write, so a sweep would run with
  write access.
- **D2. A long-lived read-only key in a profile.** The server stores a cloud
  key. A breach of the server gives a read of every cloud of every such
  target. This is weaker than ADR 164 decision 4.
- **D3. A short-lived token for each sweep (recommended).** A sweep target
  names a profile whose login block is OIDC (ADR 204). For each sweep, the
  server issues a token, and the job proxy, not the plugin, exchanges it
  for a read-only cloud credential through the federation of each cloud
  (AWS `AssumeRoleWithWebIdentity`, Azure federated credentials, Google
  Cloud workload identity federation, Kubernetes structured authentication).
  The server stores no cloud credential. It stores a signing key, which ADR
  164 open question 2 says needs a change of decision 4 and a security
  review.
  - **The token binds the code, not only the target.** The subject is
    `sweep:<namespace>/<target>/<kind>:<name>`, where `<kind>` is `plugin`
    with the plugin name, or `command` with the SHA-256 of the argument
    list. `<namespace>` and `<target>` keep the character rules of their
    names (lower-case letters, digits and `-`), and the plugin name keeps the
    rule of Option C, so no part can hold a `/` or a `:` that would shift
    the parts. The token also has the claims `plugin_digest` (or, for a
    command, `image_digest`, the digest of the OCI image of the sandbox),
    `installation` (the issuer), and `jti` set to the sweep ID. A trust
    policy that must pin the image of a command tests `image_digest`. So a holder of
    `namespace:manage-sweeps` who changes a target to a custom command, or
    to another plugin, gets another subject, and the role in the cloud does
    not match until a person in the cloud changes its trust policy. A trust
    policy can also test `plugin_digest` to pin one release. ADR 205
    decision 6 gives the subject template of a namespace; the sweep subject
    is a fixed form that a template cannot change.
  - **Bearer clouds.** For Azure, Google Cloud and Kubernetes the credential
    is a bearer token. The job proxy injects it (ADR 327 decision 5, way A)
    only on the token and API hosts that OpenDustpan lists for each cloud,
    never on a host that only the manifest names. The plugin never sees it.
  - **AWS.** The client signs each request (Signature Version 4), so the
    proxy cannot inject it (RFC 16). The job proxy serves the credential to
    the plugin on a loopback endpoint of the AWS container credentials
    provider, with a session of 15 minutes, and renews it. **This is a
    gap:** a hostile plugin can copy a read-only credential, and the
    credential works from any network for up to 15 minutes. The guide
    documents the mitigation: an `aws:SourceIp` or `aws:SourceVpce`
    condition in the trust policy of the role, so a copy does not work from
    another network, and `aws:ResourceAccount` as a data perimeter
    condition. The server cannot read the trust policy, so it does not
    enforce this.
- **D4. D1 on a pool with only the sweep purpose, D3 everywhere else
  (recommended).** D2 is not offered. A team that has no OIDC federation
  uses a pool with the sweep purpose only.

### The host identity: an operator responsibility (owner, 2026-11-07)

The review of this RFC found that a plugin could reach the write identity of
the host: ADR 137 decision 6 makes the network of the host the default egress
of a sandbox, so the instance metadata endpoint can answer; on the
`kubernetes` isolation kind the Pod has the service account of the pool; and
ADR 137 decision 4 runs the launcher in the same sandbox as the command. On a
pool with more than one purpose, that identity can write.

The owner decided on 7 November 2026: "A plugin could reach the host's write
identity. This is once again a documentation thing, it's the user's
problem." So this RFC adds no mechanism for it. The rule is:

- **A sweep plugin runs with whatever the runner host can reach.** A pool
  that has more than one purpose, or a host with a write-capable workload
  identity, exposes that identity to the plugin. The operator owns this
  choice.
- **The documentation recommends, and shows how to set up:**
  - a pool with the sweep purpose only, whose hosts have a read-only
    identity or none;
  - no write-capable workload identity on the hosts of a pool that runs
    sweeps: no instance role that can write, and on Kubernetes a sweep
    service account with no token mount and no cloud identity binding;
  - blocked metadata and workload-identity endpoints: EC2, Google Cloud and
    Azure instance metadata (`169.254.169.254`, `fd00:ec2::254`), the ECS
    task endpoint (`169.254.170.2`), EKS Pod Identity (`169.254.170.23`,
    `fd00:ec2::23`) and `168.63.129.16`, for example with the IMDS hop limit
    of 1 and a network policy;
  - pool egress through the job proxy (ADR 137 decision 6, ADR 330).
- **What stays, because the design already has it:** the token of D3 and the
  bearer tokens stay in the job proxy, as ADR 327 decision 5 does for every
  credential of the proxy; a pool with egress through the job proxy applies
  the egress rules of the sweep. No new boundary, block list or pool rule
  comes from this RFC.
- The console shows a warning on a sweep target whose pool has more than one
  purpose, in the form of ADR 347 decision 1: it names the risk and the
  setting to change. It does not block.

The residual-risk list of section "Security" records this.

### Option E. The sweep report, version 2

Version 2 keeps every rule and bound of version 1 (ADR 164 decisions 10 to
13). It changes these parts:

- **Lines of three kinds.** The first line is the header. Each other line has
  `"kind"`: `resource` (a record, as in version 1) or `coverage`. So the
  coverage does not have to fit in the header line of 64 KiB.
- **The header.**
  - `plugin`: the name, the version and the digest. The launcher replaces the
    first line of the file before the upload, and sets this field from the
    claim. The server compares it with the target. The comparison is a
    check of consistency, not a security control, because a runner that
    lies writes this field too.
- **A coverage line** is `{"kind": "coverage", "scope", "provider", "type",
  "status", "error"}`.
  - `status` is `swept`, `not-swept` (the plugin covers the type but did not
    read it, with the reason) or `unsupported` (the plugin does not cover
    the type).
  - The type namespace is the pair of `provider` and `type`, where
    `provider` is the provider of a header scope and `type` is a type that
    the manifest of the plugin lists for that provider. The manifest maps
    each such type to the IaC types of each tool, for example
    `AWS::S3::Bucket` to `aws_s3_bucket` and `aws:s3/bucket:Bucket`.
  - `error` is at most 512 bytes. A report has at most 100,000 coverage
    lines, and a type comes at most one time for each scope.
  - The server refuses a coverage line whose scope is not in the header, or
    whose provider and type are not in the manifest of the admitted
    release, and the sweep fails.
  - A coverage line has no region. A type that failed in one region of a
    scope is `not-swept` for the whole scope, so no finding of that type
    resolves in any region of it.
- **A resource line** adds:
  - `iac`: at most 2 entries, one for each tool. An entry has the tool, the
    normalized provider address (section "Keys"), the IaC type, an identity
    of at most 16 attributes and 8 KiB, and an import ID of at most 1 KiB.
    So a record stays inside a line of 64 KiB.
  - `attributeHash`: optional, SHA-256 of the canonical JSON of the
    attributes that the plugin read. The server stores it and does not read
    it until a feature needs it.
- **No downgrade.** The manifest names the report versions that the plugin
  writes. For a target that names a plugin, the server accepts only a
  report of version 2 or later, and only a version that the manifest of the
  admitted release names. A version 1 report from a plugin target fails the
  sweep. A version 1 report stays valid only for a custom command.
- **The resolve rule changes.** Version 1 resolves every finding of a complete
  scope that the sweep did not see (ADR 164 decision 13). A plugin that
  covers 276 of more than 1,500 types would then resolve findings of types
  that it never read. Version 2 resolves a finding only when the type of the
  finding maps to a `swept` coverage line of a complete scope. A finding of
  the kind "not in any unit" has the provider and type of the record that
  opened it. A finding of the kind "left behind" comes from state, and has
  an IaC type; it maps through the manifest to a coverage type, and a
  finding with no map never resolves by a sweep. A type that is `not-swept`
  or `unsupported` never resolves a finding and never shows as "nothing
  unmanaged". `FindingStore.ResolveUnseen` changes: it takes the swept types
  of each complete scope, and its suite gets a case for each status.

### Keys for providers with no cloud ID

`unmanaged.KeysOf` knows `aws`, `azure` and `gcp`, whose IDs (an ARN, an Azure
resource ID, a full resource name) name the account and often the region.
For any other provider, version 2 adds an `iac` key.

- **The form.** `iac:<provider>/<scope>/<region>/<type>/<id>`.
  - `<provider>` is the normalized provider address: the namespace and the
    name, with no registry host and in lower case, for example
    `hashicorp/aws`. So `registry.opentofu.org/hashicorp/aws` and
    `registry.terraform.io/hashicorp/aws` give one key.
  - `<type>` is the OpenTofu type. A Pulumi type of a bridged provider maps
    to its OpenTofu type with the map of the bridge (`GetMapping`), kept as
    data, so a resource in OpenTofu state and in Pulumi state gives one key.
    A Pulumi type with no OpenTofu type keeps its Pulumi token under the
    provider `pulumi/<package>`.
  - `<scope>` is the account scope, and `<region>` the region or the word
    `global`, both in the canonical form of the scope rule below.
  - `<id>` is the import ID, or the canonical JSON of the identity when the
    type has an identity schema.
- **Containment.** ADR 164 decision 13 applies to every key. The provider of
  an `iac` key, for the header, is the provider of the header scope that the
  manifest names for the provider address. A key whose scope or region is
  not in the header is refused, never matched, and counted in `outOfScope`.
  A key of a type that the scope rule makes global needs a complete scope
  of every region, as `unmanaged.Covers` says for a global resource today.
  `unmanaged.Scope` and `unmanaged.Region` get a case for the `iac` form, so
  that "the region of the key wins" holds for it too.
- **The state side.** The index makes an `iac` key for a state resource only
  when the state gives the account and the region: the identity of the
  resource (OpenTofu 1.12 stores it), or the configuration of its provider.
  When a type has no `iac` key in the index of a namespace, the server opens
  no "not in any unit" finding from an `iac` key of that type, and marks the
  type `not-matchable` in the result of the sweep. So a missing key never
  becomes a false finding.
- **One scope rule, owned by the server.** For each provider and type, the
  server holds, as data in the server repository, the rule that gives the
  canonical scope string (for example the 12 digits of an AWS account, an
  Azure subscription ID in lower case, a Google Cloud project ID, a
  Kubernetes cluster name of the target) and whether the type is regional
  or `global`. The report side and the state index both use this rule. A
  manifest may narrow it (list fewer types), never change it. When the two
  sides disagree, for example a record says `global` and the state of the
  same type gives `us-east-1`, or a scope string has another form, the
  server refuses the key and marks the type `not-matchable` for the sweep.
  A disagreement is never a "not in any unit" finding. A provider and type
  with no rule are `not-matchable` too.
- **Precedence.** A record with a cloud key (from `KeysOf` rules) is matched
  by the cloud key only. The `iac` key is used only when no cloud key rule
  exists for the provider. A record of a provider that has a cloud key rule
  (for example `aws`) whose type gives no cloud key (no ARN) is not matched
  by an `iac` key either: the match drops it, counts it, and marks its type
  `not-matchable`. So the two key forms never compete for one provider.

This is the one-way door of question 7.

### Option F. The two bridges

- **F1. The OpenTofu provider bridge** (`tofu-list`). The target names an
  admitted provider release (through the mirror of RFC 93, with the
  provider's own signature) and its list types. The bridge starts the
  provider with the handshake of `go-plugin`, implemented as in ADR 176
  decision 6, calls `GetProviderSchema`, `ConfigureProvider` (with the
  credential of Option D), then `ListResource` for each type, with
  `include_resource_object` set only when the key rule needs an attribute,
  for example `arn`. It writes one record for each event. The Go code comes
  from `docs/plugin-protocol/tfplugin6.10.proto` of OpenTofu (MPL-2.0), never
  from the copy in hashicorp/terraform.
  - **Egress.** The bridge runs a second binary, the provider, which signs
    its own requests and calls many regional endpoints. For AWS that is in
    practice every `<service>.<region>.amazonaws.com` host. The egress rules
    of a `tofu-list` sweep come from the server, not from the manifest: only
    the endpoints of the services of the listed types, in the regions of the
    target, plus the token hosts of the cloud. The rule list is still wide,
    so the AWS conditions of Option D apply in full.
- **F2. The Pulumi provider bridge** (`pulumi-list`). The same, with the
  Pulumi protocol: `GetSchema` for the resources with `listInputs`,
  `Configure`, then `List`. A Pulumi provider plugin may also need an engine
  address for its logs; the bridge serves a small log service, or the
  provider refuses to start. A test against a real provider must show which.
  The egress rules are made as for F1.
  - **The ID of a bridged provider is best-effort.** For a provider that
    bridges a Terraform provider, the bridge's own design notes call the ID
    of a list result "a best-effort string handle", and say that it is not
    proof that an import takes the same string
    (`docs/specs/terraform-list-runtime-semantics.md` and
    `terraform-list-runtime-rollout.md` in pulumi-terraform-bridge). An ID
    that differs from the `id` in Pulumi state would give a false finding of
    the kind "not in any unit". So `pulumi-list` never uses such an ID alone
    to open or resolve a finding. For a type of a bridged provider it marks
    the coverage `not-swept`, with the reason, unless it also reads the
    resource (`Read`) and gets a cloud key (for example an ARN). The guide
    says: for a bridged provider, run `tofu-list` on the provider that it
    bridges.
- **F3. No bridges, first-party plugins only.** Less code, but the reach of
  the owner's aim stops at four clouds.

### Option G. First-party plugins

`aws-resource-explorer`, `azure-resource-graph`, `gcp-asset-inventory` and
`kubernetes`. Each is a plugin in `sweep-plugins`, each with a fixture set of
recorded API answers for `sweepplugintest`, and each gives the IaC type map
for the types it knows (the map of `aws_s3_bucket` to `AWS::S3::Bucket` comes
from the published type lists of each tool, kept as data in the repository).
The Kubernetes plugin reads aggregated discovery, lists each served kind with
`limit` and `continue`, starts again on `410 Gone` up to a fixed count, and
marks each kind that it may not list as `not-swept`.

## Proposal

1. **A2**: a process contract, `describe` and `sweep`, with a manifest and the
   report of version 2. The contract has its own version, apart from the
   report version.
2. **B1**: the repository `OpenDustpan/sweep-plugins`, with its own ADR.
3. **C1**: the archive set. Integrity is fixed in code: only the admitted
   bytes, checked by the server and again by the launcher, archive rules,
   revocation on both sides, and a start from a file descriptor. Provenance
   is facts (the signature among them) with a policy that the administrator
   writes (ADR 351), and example policies that bind keys to plugin names.
   The runner may pin its own keys.
4. **D4**: a short-lived token for each sweep on a pool with more than one
   purpose, whose subject binds the target and the plugin; the host identity
   only on a pool with the sweep purpose only; never a long-lived key.
5. **The host identity is the operator's responsibility** (owner,
   2026-11-07). A plugin runs with whatever the runner host can reach. The
   documentation recommends a pool with the sweep purpose only, hosts with no
   write identity and blocked metadata endpoints, and shows how to set each
   up. The console warns on a target whose pool has more than one purpose.
6. **E**: report version 2, with coverage lines, no downgrade, the resolve
   rule for each type, and an `iac` key that holds the account and the
   region.
7. **F1 and F2** after **G**: the four first-party plugins first, then the two
   bridges.
8. **A custom command stays.** A target names either an admitted plugin or a
   command (ADR 164 decision 8). A command gets the same credential rule
   (D4) and the same sandbox, but no signature check, and the console says so
   on the target. Its token has the subject kind `command` with the hash of
   its arguments, so a cloud role that trusts a plugin does not trust it.
   It may write a report of version 1 or 2.

### The term

The glossary has the old Go name `inventory.Sweeper` for the retention
expiry, and it lists "sweep" under _Avoid_ for anything but the Sweep
feature. "Sweeper" for the program would make a second meaning. This RFC uses
**sweep plugin**. The glossary entry would read: "A signed program that a
runner starts for a cloud sweep. It lists the resources of an account and
writes a sweep report. It has a manifest and implements the sweep plugin
contract."

## Security

A sweep plugin is third-party code with a cloud credential. The inputs from
outside the process are the plugin archive, the manifest, the output of
`describe`, the report, the configuration of the target, and the answers of
the cloud APIs to the plugin. For each, the five steps of ADR 250:

| Input | 1. Check at the boundary | 2. Contain | 3. Limit | 4. Authorize at use | 5. Fail closed |
| --- | --- | --- | --- | --- | --- |
| The archive | Integrity, fixed: the hashes against `SHA256SUMS`, the size and the paths. Provenance: the signature result and the other signals as facts, read by the administrator's gate (ADR 351). | Stored by digest; served only as the admitted bytes; opened only in the sandbox. | 256 MiB compressed, 1 GiB unpacked, 64 regular files, no links, no `..`. | `server:admit-sweep-plugin` (new) and the admission gate; `namespace:manage-sweeps` to name it in a target. | A broken archive, a hash that differs or a revoked release: no admission and no run. A deny of the gate names the policy and the facts. An audit entry for each. |
| The archive at the runner | The launcher checks the SHA-256 against the claim and `SHA256SUMS`, and both revocation lists, with no trust in the server's check; when the runner pins keys, also the signature and the names. | Started from the file descriptor of the check (`execveat`); for `firecracker`, checked inside the microVM. | The same bounds. | The claim names the digest; the launcher refuses another. | A mismatch ends the sweep as `failed`, with the reason. |
| The manifest and `describe` | A strict JSON Schema; unknown fields refused; a manifest version that the launcher lists; the output of `describe` must equal the signed manifest. | `describe` runs with no network and no credential. | 1 MiB; a timeout of 30 seconds. | - | Any difference, or no shared contract version, fails the sweep. |
| The host | The operator's choice (owner, 2026-11-07): the pool, its purposes and the identity of its hosts. | The sandbox of ADR 137; the documentation shows how to block metadata endpoints and to drop the service account token. | The CPU, memory and time limits of ADR 137 decision 7. | The pool purpose, checked at the claim. | The console warns on a target whose pool has more than one purpose. |
| The egress of the plugin | Rules made by the server from the cloud, the services of the listed types and the regions of the target; the manifest cannot widen them. | On a pool with egress through the job proxy: only the proxy. On a pool with the network of the host: the operator's choice (see "The host identity"). | The rate and the count of requests of the proxy. | The proxy checks each request against the rules of this sweep. | A request outside the rules is denied and logged. |
| The credential | A profile login block of the kind OIDC; a static key is refused. | The token of D3 and bearer tokens only in the job proxy; the AWS credential only on a loopback endpoint, for 15 minutes. | The expiry of the token is at most the timeout of the target. | The cloud role trusts only the subject `sweep:<namespace>/<target>/<kind>:<name>`, and may test `plugin_digest`. | No credential: the sweep fails before the plugin starts. |
| The report | Version 2 or later for a plugin target, and only a version of the manifest; the parser refuses unknown fields and unknown line kinds. | Written to a file that the launcher reads; the launcher replaces the header line; uploaded as a stream; encrypted at rest (ADR 164 decision 12). | The bounds of ADR 164 decision 11, plus the coverage and `iac` bounds; the launcher stops the upload at the bound. | The upload needs the session key of the claim (ADR 131). | A report over a bound, with an error or of a lower version fails the sweep; no finding resolves. |
| A key in a record | The `iac` form; a scope and a region inside the header. | The containment of ADR 164 decision 13 for every key. | The bounds of an `iac` entry. | The match reads only the namespace and its linked namespaces (ADR 163). | A key outside the header is refused and counted, never matched; a type with no `iac` key in the index opens no finding. |

What a hostile plugin can still do, inside these layers:

- Everything that ADR 164 decision 17 lists for a runner that lies: hide,
  make or change findings inside the scopes of its target.
- Mark a type `swept` that it did not read, so that findings of that type
  resolve. The coverage is a claim of the plugin. The console shows the
  plugin that made each resolve.
- Read every resource that its read-only role can read, and send the data to
  a host that the egress rules of its sweep allow. The server makes those
  rules, and a manifest cannot widen them. On a cloud API that many tenants share (for
  example the S3 endpoint), a request to a bucket of the attacker is
  allowed by a rule on the host. This is the residual risk of any read-only
  plugin. The signature, the review of community plugins and the data
  perimeter conditions on the cloud role reduce it. They do not remove it.
- On AWS, copy a read-only credential of at most 15 minutes. The documented
  `aws:SourceIp` or `aws:SourceVpce` condition on the role stops its use
  from another network, where the operator sets it (Option D).
- **Move a budget gate.** A spend plugin can raise or lower the spend of its
  scopes, and a gate on the `budget` fact (ADR 226 decision 8) then blocks
  or lets pass changes. The fact names the plugin and the report, and the
  gate policy decides what that origin is worth (section "Spend and cost on
  the same plugins").
- **Reach the identity of the host.** A plugin runs with whatever the runner
  host can reach. On a pool with more than one purpose, or on a host with a
  write-capable workload identity, the plugin can use that identity, and so
  can write to the cloud. The owner decided on 7 November 2026 that this is
  the operator's responsibility, to document and not to prevent in the
  design (section "The host identity").

## The ADR 164 amendment

A draft for the ADR that accepts this RFC. It changes ADR 164 and adds to
ADR 343:

- **Decision 4 changes.** A sweep on a pool whose purposes include `jobs`
  gets its credential only from a profile login block of the kind OIDC, as a
  token for each sweep with the subject
  `sweep:<namespace>/<target>/<kind>:<name>` and the claims
  `plugin_digest`, `installation` and `jti`. The job proxy holds the token,
  never the plugin. A pool with the sweep purpose only may still use the
  identity of its hosts. No sweep uses a long-lived key. A plugin runs with
  whatever the runner host can reach: the identity of the host on a pool
  with more than one purpose is the operator's responsibility (owner,
  2026-11-07), and the documentation recommends a pool with the sweep
  purpose only, hosts with no write identity and blocked metadata
  endpoints. Open question 2 of ADR 164 is answered.
- **Decision 7 changes.** A sweep target names either an admitted sweep
  plugin (name, version, digest, and a configuration of at most 64 KiB
  with no secret) or a command.
- **Decision 9 changes.** OpenDustpan ships sweep plugins from the repository
  `OpenDustpan/sweep-plugins`, never in the server. Open question 1 is
  answered.
- **Decisions 10 and 13 extend.** The report has version 2, with coverage
  lines and the `iac` entries. A plugin target accepts only version 2 or
  later. A finding resolves only when its type is `swept` in a complete
  scope. The containment of decision 13 applies to the `iac` key, which
  holds the account scope and the region.
- **ADR 343 decision 5 extends.** A converter is a sweep plugin.

## The new-repository ADR

ADR 343 decision 6 gives a repository only to the runner. The ADR for
`OpenDustpan/sweep-plugins` must say:

- why the code is not built together with the server: it is a set of
  programs that the launcher starts, with their own release line, and the
  server links none of it (the owner's rule, "one repo for code built
  together");
- the gates of this repository, copied, as ADR 170 decision 4;
- no import across the boundary, as ADR 170 decision 5;
- who reviews a community plugin, and what the review checks: the manifest,
  the egress hosts, the read-only permissions, the fixtures;
- that the project key signs only what the CI of the repository builds;
- the dependencies that need the owner's approval: the cloud SDKs, gRPC and
  protobuf for the bridges, the MPL-2.0 `.proto` files, and OpenPGP in the
  runner.

## One-way doors

- **The contract version** (`describe`, `sweep`, the request, the manifest,
  the exit codes). Once a third party ships a plugin, a change breaks it.
  The launcher must state which contract versions it runs, as ADR 132 does
  for the runner protocol.
- **The report version 2.** A server reads every version that a released
  plugin writes (ADR 72). The coverage values and the `iac` entry are hard to
  change later.
- **The spend report version 1 and the plugin kinds** (`sweep`, `spend`,
  `estimate`). The follow-up RFC decides the spend report, but the manifest
  field `kinds` ships with the first contract version.
- **The `iac` key in the resource index**, with its provider address
  normalization, its account and region parts and its precedence. Once findings are stored with it,
  a change of its form needs a new match of every namespace.
- **The project signing key.** The example policies and many installations
  will trust it. A rotation and a
  revocation need a plan before the first release.
- **The repository name** and the module path, in every import of a
  third-party plugin.

Two-way doors: the list of first-party plugins, the bridges, Sigstore later,
the size limits.

## Spend and cost on the same plugins (RFCs 45 and 67)

The owner decided on 7 November 2026 that FinOps (cost estimation, RFC 45, and
the integration with spend and cost tools, RFC 67) goes into the same
milestone as sweep plugins. The reason, in the product manager's words: both
answer "what really exists in the cloud, and what does it cost". They read the
same data for each resource from the inventory and billing APIs of a cloud,
with the same plugins, runners and short-lived read credentials, keyed by the
same resource identity.

This section maps RFCs 45 and 67 onto the plugin model. It does not design the
FinOps feature. Its open points are for a follow-up RFC.

### What is decided already for cost

- ADR 144 gives a cost port: an estimate of a plan (`cost.Estimate`), actual
  spend (`cost.SpendSource`) and budgets. A budget never blocks; only a
  policy can block (decisions 13 and 14).
- ADR 144 decision 5 gives two ways in for an estimate: pull (the
  `cost.Estimator` port in the server) and push (a step of the role `tool`
  in a runner job). ADR 259 ships the push route first. No estimator is in
  the binary (ADR 144 decision 6).
- ADR 144 decisions 7 to 9: the server reads spend through the
  `cost.SpendSource` port, once an hour for each namespace with a budget. An
  adapter finds the resources of a unit by a tag that holds the unit ID, or
  by its own mapping.

### The map

| Need (RFC) | Today | With plugins |
| --- | --- | --- |
| The spend of each resource and each account (RFC 67, ADR 144 decision 7) | A `SpendSource` adapter, called by the server, with credentials for a billing API that the server would hold | A **spend run**: a plugin with the kind `spend` runs on a runner, with a short-lived read credential (Option D), and uploads a **spend report**. The server reads the latest spend report of a namespace in place of a call to a tool. |
| Which unit a cost belongs to (ADR 144 decision 8) | A tag with the unit ID on each resource, written by the team | The same resource keys as a sweep (`unmanaged.KeysOf`, and the `iac` key of Option E) match each spend line against the resource index. A tag stays a second way. A line with no match is spend of the namespace with no unit. |
| The cost of unmanaged resources | None | A spend line whose key matches a finding of the kind "not in any unit" gives that finding a monthly cost. This is new value that neither RFC has today. |
| The estimate of a plan (RFC 45, ADR 259) | The push route from a job step | An estimator can be a signed plugin with the kind `estimate`, run as a step of the role `tool` in a job, that pushes through the route of ADR 259. It reads the masked plan and a price list, and needs no cloud credential. It runs on a pool with the purpose `jobs`, not `sweeps`. |
| Allocation labels and events for a cost tool (RFC 67) | Open | Not changed by this RFC. |

### How the contract extends

The contract of Option A gets one more command, and the manifest names the
kinds that a plugin implements:

- The manifest has `kinds`: a list of `sweep`, `spend` and `estimate`. A
  plugin implements one or more kinds. The admission, the signature, the
  sandbox, the egress rules and the credential rules are the same for every
  kind.
- `spend --request <file> --report <file>` writes a **spend report**. The
  request names the account scopes and one month.
- `estimate` is left to the follow-up RFC, because ADR 259 already gives its
  way in.

The spend report is its own format, not a part of the sweep report version 2.
The reasons: a sweep report says what exists now, a spend report says what a
month cost; they have different bounds, different schedules and a different
read action (`namespace:read-cost`, ADR 144 decision 15). A proposal for its
shape, for the follow-up RFC to decide:

- JSON Lines, with the format name `opendustpan-spend-report` and version 1.
- The header has the plugin (written by the launcher), the month, the scopes
  with `complete` and `error`, as in a sweep report, and the currency of
  each scope.
- Each record has the provider, the resource key (or no key for an account
  line), the scope, the region, the service, the `kind` (`actual` or
  `forecast`), and the amount as a decimal string in an ISO 4217 currency,
  with the bounds of `cost.Money` (ADR 144 decision 1).
- The server treats it as unverified facts, as ADR 164 decision 17 says for
  a sweep report. A hostile or buggy plugin can make a cost too high or too
  low in its own scopes.
- **A spend report can block a change, through a gate.** A budget alone
  never blocks (ADR 144 decision 13). But ADR 226 decision 8 makes a cost
  gate an ordinary gate at `change.planned` on the `cost` and `budget`
  facts, and the `budget` fact comes from spend. So a spend plugin can push
  the level of a budget to `exceeded` and a gate then refuses changes in its
  namespace, or it can hold the level at `ok` and a gate lets changes pass.
  The rule of this RFC: the `budget` fact names the spend report and the
  plugin (name, version, digest) that it came from, with the time of the
  report, and marks its origin as a plugin on a runner, an unverified fact
  (ADR 351 decision 4). The gate and the person who reads its decision see
  that origin. Whether a gate may block on a fact of that origin is the
  administrator's policy. The residual-risk list records this.

### Open points for a follow-up RFC

- **The billing sources and their keys.** Which billing API of each cloud a
  first-party spend plugin reads, and whether its lines carry a resource ID
  that matches the keys of the resource index. This RFC did not check the
  billing APIs. The follow-up RFC must check them against primary sources.
- **ADR 144 decisions 7 to 9.** With spend runs, the server no longer calls
  a spend tool. The schedule of decision 9 starts a spend run on a runner
  instead. The follow-up RFC writes this change.
- **The target and the purpose.** Whether a spend run uses a sweep target or
  a target of its own, and whether the pool purpose `sweeps` also covers
  spend runs or a new purpose comes (ADR 343 lets a pool have several).
- **Billing credentials.** A billing role is not the read-only role of a
  sweep (for example, the billing data of AWS is in the management account).
  Whether one plugin run may hold both.
- **Third-party FinOps tools** (the research task of RFC 67): whether a
  plugin reads a cost tool, for example the export of a FinOps product, in
  place of a cloud billing API.
- **The estimate kind**, and whether an `estimate` plugin replaces the first
  pull adapter that ADR 259 decision 3 leaves open.
- **The origin of the `budget` fact.** The fact schema field that names the
  spend report and the plugin, and how a gate reads it.
- **A subject kind for spend runs.** A spend run needs its own subject
  form, for example `spend:<namespace>/<target>/plugin:<name>`, so that a
  billing role never trusts a sweep run and a read-only role of a sweep
  never trusts a spend run.

## Where it fits in the roadmap

A proposal only; this RFC does not edit `docs/roadmap.md`.

- **A new milestone, "Cloud inventory: sweeps and spend", after milestone 9
  and before the agents milestone.** It takes the number 10, and the agents
  milestone moves to 11. The name follows the owner decision of 7 November
  2026 that puts FinOps in the same milestone. It needs runners and the
  runner protocol (milestone 5), the implementation of ADR 164 (the sweep,
  the upload and the match), the job proxy of ADR 327 and ADR 330 in the
  runner, the OIDC issuer of ADR 204, and the cost port of ADR 144.
- **Scope:**
  - Sweeps: the contract and report version 2; the repository with the SDK
    and `sweepplugintest`; admission and the launcher checks; D4; the four
    first-party sweep plugins. The two bridges are the second part of the
    same milestone, after the first-party plugins (question 8).
  - Spend: the kind `spend` in the contract and the spend report version 1;
    the spend run on a runner; the match of spend lines against the resource
    index; the cost of a finding; at least one first-party spend plugin,
    chosen by the follow-up RFC.
  - Out of scope: the estimate kind, allocation labels and events for a cost
    tool. They stay in RFCs 45 and 67.
- **Design first:** this RFC, and the follow-up RFC for spend, come before
  any code of the milestone.
- **Exit test (sketch):** on Linux, on PostgreSQL, through the real binary,
  with a pool that has both purposes:
  1. A signed Kubernetes plugin sweeps a kind cluster with 3 resources in no
     unit. Pass: 3 findings, coverage `swept` for each kind, `not-swept` for
     Secrets with the `view` role.
  2. A plugin with a changed byte fails before it starts, with an audit
     entry.
  3. The console warns on the sweep target, because its pool has more than
     one purpose. A second run on a pool set up as the guide recommends
     (the sweep purpose only, the metadata endpoints blocked with the
     documented network policy) shows that a request of the test plugin to
     `169.254.169.254` fails. This step tests the documented setup, not a
     rule of the design.
  4. A report that marks a type `unsupported` resolves no finding of that
     type.
  5. A signed test spend plugin uploads a spend report with 3 lines: one for
     a resource in a unit, one for a resource of a finding, one for the
     account. Pass: the unit shows its spend, the finding shows its monthly
     cost, and the namespace shows the account line as spend with no unit.

## Open questions for the owner

1. **A new repository?** Should sweep plugins live in a new repository,
   `OpenDustpan/sweep-plugins`, as a second exception to ADR 343 decision 6?
   Alternative: the provider kit repository. Recommended: the new
   repository.
   _ELI5: we want a separate toy box where anyone can add a new toy without
   opening the main box. The rule says only one toy has its own box today.
   May this one have a box too?_
2. **A process contract or gRPC?** Should a sweep plugin be a program that
   reads a file and writes a file (`describe`, `sweep`)? Alternative: a gRPC
   service with the `go-plugin` handshake. Recommended: the process
   contract.
   _ELI5: we can talk to the helper on the phone the whole time, or give it
   a note and get a list back. It only ever makes one list, so the note is
   simpler and anyone can write one._
3. **Credentials on a pool with two purposes.** On a pool that also applies
   changes, should a sweep get only a short-lived token for that one sweep
   (OIDC, through a profile), whose subject names the target and the plugin?
   Alternatives: the identity of the host, or a stored read-only key in a
   profile. Recommended: the short-lived token, and never a stored key. (The
   identity of the host stays the operator's responsibility, by the owner's
   decision of 7 November 2026.)
   _ELI5: when the robot only counts things, we give it a visitor badge that
   works only for this one count and this one helper. If the robot also
   carries a master key, that is the owner of the robot's choice, and our
   guide says to leave it at home._
4. **The AWS gap.** AWS signs each request in the client, so the plugin sees
   a read-only credential of 15 minutes. Options: (a) document an
   `aws:SourceIp` or `aws:SourceVpce` condition on the role and enforce
   nothing; (b) enforce the condition through a statement of the operator
   on the target; (c) AWS sweeps only on a pool with the sweep purpose only.
   Recommended: (a), in line with the owner's decision on the host identity.
   _ELI5: for one shop, the visitor must hold the badge in their own hand.
   We can write in the guide how to make the badge work only inside your
   building, or make you promise it, or send only a robot that never builds
   things. We suggest the guide._
5. **Dependencies.** May these dependencies come in, each in the repository
   named? (a) An OpenPGP library (`github.com/ProtonMail/go-crypto`, already
   approved for the server registry by ADR 349) in the launcher of the
   runner repository, only for keys pinned in the runner configuration;
   (b) the cloud SDKs of AWS, Azure and Google Cloud and the Kubernetes
   client in `sweep-plugins`; (c) gRPC, protobuf and the MPL-2.0 `.proto`
   files of OpenTofu and of `go-plugin`, and the Pulumi `provider.proto`
   (Apache-2.0), in `sweep-plugins` for the two bridges. Alternative for (a):
   no pinned keys on the runner, so only the server checks a signature.
   Recommended: approve all three.
   _ELI5: to build the helpers we need some tools from other people. Each
   tool goes only into the box that needs it. May we use them?_
6. **Resolve only what was swept.** Should a finding resolve only when the
   plugin says it swept that type? Alternative: keep the rule of version 1,
   where a complete scope resolves every finding that the sweep did not see.
   Recommended: resolve only what was swept.
   _ELI5: if you looked only under the bed, you may not say that the closet
   is clean._
7. **A key for every provider.** Should the resource index get a key of the
   form provider, account, region, type and ID, with one scope rule that the
   server owns (section "Keys for providers with no cloud ID"), so that
   sweeps can match resources of any provider in the registries?
   Alternative: keys only for AWS, Azure and Google Cloud, as today, and
   every other provider stays `not-matchable`. Recommended: the new key. It
   cannot change easily later.
   _ELI5: today we can only recognize three kinds of name tags. A general
   name tag lets us recognize every kind, but once we print them, we keep
   them._
8. **The bridges in the first slice?** Should the generic bridges to
   OpenTofu providers and Pulumi providers come in the same milestone as the
   four first-party plugins? Alternative: "Later". Recommended: the same
   milestone, as its second part after the four first-party plugins, because
   the bridges are the reach to the registries that the owner asked for.
   _ELI5: first we build four helpers that know the big clouds, then a
   translator that lets every provider help. We suggest both in the same
   season, the helpers first._

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write waits
  for another installation (ADR 248 decisions 3, 6 and 9). Targets, sweeps and
  findings stay namespace data. The admission of a plugin is installation
  data, as a trusted key is; a namespace on another installation needs the
  plugin admitted there too.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
