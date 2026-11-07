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
   digest, checks its signature as the registry checks a provider, and runs
   it in the sandbox with read-only, short-lived credentials and egress
   only to the hosts of its signed manifest. The server treats the report as
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
| ADR 250 | Defense in depth for every input from outside the process. | Section "Security". |
| ADR 347 | Warn, do not block, for choices that the operator owns. Security controls keep their own rules. | The signature check and the sandbox are security controls. |


ADR 343 and ADR 347 are in pull request #2562, which is open on the day of
this RFC. This RFC reads them as the owner's decisions of 7 November 2026. If
their text changes before the merge, this RFC changes with them.

## Facts from primary sources

Checked on 7 November 2026, from the source files, the change logs and the
official documentation. Each fact names its source. "Not found" means that the
search found nothing. It does not mean that the thing does not exist.

### The provider plugin protocol of OpenTofu and Terraform

- **`ListResource` exists in protocol versions 5.10 and 6.10.** It is a
  server stream:
  `rpc ListResource(ListResource.Request) returns (stream ListResource.Event)`.
  The request has `type_name`, `config`, `include_resource_object` and
  `limit`. Each event has `identity`, `display_name`, an optional
  `resource_object` and diagnostics. `GetProviderSchema` returns
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
  that can be passed to Read". The Pulumi change log puts it in 3.233.0
  (30 April 2026). A resource that supports it has `listInputs` in the
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
| CloudQuery AWS, Google Cloud and Azure plugins | paid since April 2024; no license stated on the hub | - | May not use. A paid plugin with no stated license is impossible for us. |
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
warning when the hub sends none. Source: the documentation pages of each. Our
version is the stronger one: no check, no run (section "Security").

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

- **C1. The archive set, admitted into the server (recommended).** A sweep
  plugin release is the archive set of ADR 269: one zip for each operating
  system and architecture, `SHA256SUMS`, and its OpenPGP signature
  `SHA256SUMS.sig`. The zip holds the binary and the manifest. The project
  CI of `sweep-plugins` builds and signs the first-party and accepted
  community plugins with the project key.
  - An operator admits a release into the installation, from a file upload
    or from the release page of the project. The server checks the hashes
    and the signature against the trusted keys of the installation, with
    the code of `pkg/registry/pgpverify` (ADR 349), and stores the archives
    by digest.
  - A sweep target names an admitted plugin by name, version and digest.
  - The launcher fetches the archive from the server by digest, checks the
    SHA-256 itself, checks the signature itself, opens it in the sandbox
    and starts the binary by a file descriptor, not by a path, so that no
    other process can change the file between the check and the start.
  - Costs: the launcher needs an OpenPGP library, a dependency that needs the
    owner's approval for the runner repository (ADR 316 approves it only for
    the provider kit, ADR 349 for the server registry).
- **C2. Sigstore keyless signatures and SLSA provenance.** Stronger
  provenance, but a new trust root, a transparency log on the public
  internet (an installation with no internet cannot check it online) and a
  larger dependency. A later addition, not the first.
- **C3. The OCI image of the team.** The plugin is in the image of the
  sandbox, as the command of ADR 164 is today. No signature check by
  OpenDustpan. This stays for a custom command (see "Proposal"), not for an
  admitted plugin.

A third-party plugin from outside the project repository needs a key that the
operator trusts. The operator adds such a key to the installation. A plugin
with no valid signature never runs. This is a security control, so ADR 347
decision 4 applies, not "warn, do not block".

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
  server issues a token with the subject `sweep:<namespace>/<target>` and an
  expiry of at most the timeout of the target. The launcher, not the plugin,
  exchanges it for a read-only cloud credential through the federation of
  each cloud (AWS `AssumeRoleWithWebIdentity`, Azure federated credentials,
  Google Cloud workload identity federation, Kubernetes structured
  authentication). The role in the cloud trusts only that subject, so the
  cloud enforces "read-only" and "this sweep only". The server stores no
  cloud credential. It stores a signing key, which ADR 164 open question 2
  says needs a change of decision 4 and a security review.
  - For Azure, Google Cloud and Kubernetes the credential is a bearer
    token. The job proxy injects it (ADR 327 decision 5, way A), and the plugin
    never sees it.
  - For AWS the client signs each request (Signature Version 4), so the
    proxy cannot inject it (RFC 16). The launcher serves the short-lived
    credential to the plugin on a loopback endpoint of the AWS container
    credentials provider, and renews it. The plugin sees a read-only
    credential of at most one hour. **This is a gap:** a hostile plugin
    can copy it within the egress rules. The guide names the limits:
    egress only to the hosts of the manifest, AWS data perimeter conditions
    on the role (`aws:ResourceAccount`), and the role session duration.
- **D4. D1 on a pool with only the sweep purpose, D3 everywhere.** The
  recommendation: a sweep on a pool whose purposes include `jobs` must use
  D3. The launcher refuses the host identity there: the sandbox blocks the
  instance metadata addresses (`169.254.169.254`, `fd00:ec2::254`) and
  mounts no service account token. A pool with the sweep purpose only may
  still use D1, as ADR 164 decision 4 says. D2 is not offered. A team that
  has no OIDC federation uses a pool with the sweep purpose only.

### Option E. The sweep report, version 2

Version 2 keeps every rule and bound of version 1 (ADR 164 decisions 10 to
13). It adds:

- **In the header:**
  - `plugin`: the name, the version and the digest that the launcher ran.
    The launcher writes this field, not the plugin. The server compares it
    with the target and refuses a report where they differ.
  - `coverage`: for each scope, a list of `{type, status, error}`, where
    `status` is `swept`, `not-swept` (the plugin covers the type but did not
    read it, with the reason) or `unsupported` (the plugin does not cover
    the type). At most 10,000 entries in a report.
- **In a record:**
  - `iac`: at most 4 entries, one for each tool, for example
    `{"tool": "opentofu", "provider": "registry.opentofu.org/hashicorp/aws",
    "type": "aws_s3_bucket", "identity": {...}, "importId": "..."}` and
    `{"tool": "pulumi", "package": "aws", "type": "aws:s3/bucket:Bucket",
    "importId": "..."}`. An identity has at most 32 attributes of at most
    1 KiB each.
  - `attributeHash`: optional, SHA-256 of the canonical JSON of the
    attributes that the plugin read. The server stores it and does not read
    it until a feature needs it.
- **The resolve rule changes.** Version 1 resolves every finding of a complete
  scope that the sweep did not see (ADR 164 decision 13). A plugin that
  covers 276 of more than 1,500 types would then resolve findings of types
  that it never read. Version 2 resolves a finding only when its type has
  the status `swept` in a complete scope. A type that is `not-swept` or
  `unsupported` never resolves a finding and never shows as "nothing
  unmanaged". A version 1 report has no coverage, so for it the rule of
  version 1 stays.
- **A key for providers with no cloud ID.** `unmanaged.KeysOf` knows `aws`,
  `azure` and `gcp`. For any other provider, version 2 lets the match use
  the `iac` entry: the key `iac/<tool>/<provider>/<type>/<import ID>`, which
  the index can also read from state (the `id` of an OpenTofu resource, the
  `id` of a Pulumi resource). The identity of OpenTofu 1.12 state can give
  a second key later. This is a change of the resource index of ADR 163,
  and a one-way door (section "One-way doors").

### Option F. The two bridges

- **F1. The OpenTofu provider bridge** (`tofu-list`). The target names an
  admitted provider release (through the mirror of RFC 93, with the
  provider's own signature) and its list types. The bridge starts the
  provider with the handshake of `go-plugin`, implemented as in ADR 176
  decision 6, calls `GetProviderSchema`, `ConfigureProvider` (with the
  credential of Option D), then `ListResource` for each type, with
  `include_resource_object` set only when the key rule needs an attribute,
  for example `arn`. It writes one record for each event. The Go code comes
  from `docs/plugin-protocol/tfplugin6.10.proto` of OpenTofu (MPL-2.0).
- **F2. The Pulumi provider bridge** (`pulumi-list`). The same, with the
  Pulumi protocol: `GetSchema` for the resources with `listInputs`,
  `Configure`, then `List`. A Pulumi provider plugin may also need an engine
  address for its logs; the bridge serves a small log service, or the
  provider refuses to start. A test against a real provider must show which.
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
3. **C1**: the archive set, admitted into the installation, checked by the
   server and again by the launcher, started by a file descriptor.
4. **D4**: a short-lived token for each sweep on a pool with more than one
   purpose; the host identity only on a pool with the sweep purpose only;
   never a long-lived key.
5. **E**: report version 2, with coverage and the resolve rule for each type.
6. **F1 and F2** after **G**: the four first-party plugins first, then the two
   bridges.
7. **A custom command stays.** A target names either an admitted plugin or a
   command (ADR 164 decision 8). A command gets the same credential rule
   (D4) and the same sandbox, but no signature check, and the console says so
   on the target. ADR 343 lets a pool run jobs too, and a job also runs the
   code of the team, so a command is no new risk on such a pool.

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
| The archive | The server checks the hashes and the signature against the trusted keys at admission. | Stored by digest; opened only in the sandbox. | A size limit on the archive (proposal: 256 MiB). | `server:admit-sweep-plugin` (new) at admission; `namespace:manage-sweeps` to name it in a target. | No valid signature: no admission and no run, and an audit entry. |
| The archive at the runner | The launcher checks the SHA-256 and the signature again, without trust in the server's check. | Started from a file descriptor in the sandbox. | The same size limit. | The claim names the digest; the launcher refuses another. | A mismatch ends the sweep as `failed`, with the reason. |
| The manifest and `describe` | A strict JSON Schema; unknown fields refused; the output of `describe` must equal the signed manifest. | `describe` runs with no network and no credential. | 1 MiB; a timeout of 30 seconds. | - | Any difference fails the sweep. |
| The egress of the plugin | Only the hosts of the signed manifest, through the job proxy. | A network namespace that reaches only the proxy (ADR 137 decision 6); no instance metadata address. | The rate and the count of requests of the proxy. | The proxy checks each request against the rules of this sweep. | A request outside the rules is denied and logged. |
| The credential | A profile login block of the kind OIDC; a static key is refused. | Bearer tokens only in the proxy; the AWS credential only on a loopback endpoint. | The expiry of the token is at most the timeout of the target. | The cloud role trusts only `sweep:<namespace>/<target>`. | No credential: the sweep fails before the plugin starts. |
| The report | Version 1 or 2 only; the parser refuses unknown fields; the header must name the plugin of the claim. | Written to a file that the launcher reads; uploaded as a stream; encrypted at rest (ADR 164 decision 12). | The bounds of ADR 164 decision 11, plus the coverage and `iac` bounds; the launcher stops the upload at the bound. | The upload needs the session key of the claim (ADR 131). | A report over a bound or with an error fails the sweep; no finding resolves. |

What a hostile plugin can still do, inside these layers:

- Everything that ADR 164 decision 17 lists for a runner that lies: hide,
  make or change findings inside the scopes of its target.
- Mark a type `swept` that it did not read, so that findings of that type
  resolve. The coverage is a claim of the plugin. The console shows the
  plugin that made each resolve.
- Read every resource that its read-only role can read, and send the data to
  a host of its manifest. On a cloud API that many tenants share (for
  example the S3 endpoint), a request to a bucket of the attacker is
  allowed by a rule on the host. This is the residual risk of any read-only
  plugin. The signature, the review of community plugins and the data
  perimeter conditions on the cloud role reduce it. They do not remove it.
- On AWS, copy a credential of at most one hour (Option D).

## The ADR 164 amendment

A draft for the ADR that accepts this RFC. It changes ADR 164 and adds to
ADR 343:

- **Decision 4 changes.** A sweep on a pool whose purposes include `jobs`
  gets its credential only from a profile login block of the kind OIDC, as a
  token for each sweep with the subject `sweep:<namespace>/<target>`. The
  sandbox blocks the identity of the host. A pool with the sweep purpose
  only may still use the identity of its hosts. No sweep uses a long-lived
  key. Open question 2 of ADR 164 is answered.
- **Decision 7 changes.** A sweep target names either an admitted sweep
  plugin (name, version, digest, and a configuration of at most 64 KiB
  with no secret) or a command.
- **Decision 9 changes.** OpenDustpan ships sweep plugins from the repository
  `OpenDustpan/sweep-plugins`, never in the server. Open question 1 is
  answered.
- **Decisions 10 and 13 extend.** The report has version 2, with coverage for
  each type and the `iac` entries. A finding resolves only when its type is
  `swept` in a complete scope.
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
- **The `iac` key in the resource index.** Once findings are stored with it,
  a change of its form needs a new match of every namespace.
- **The project signing key.** Every installation trusts it. A rotation and a
  revocation need a plan before the first release.
- **The repository name** and the module path, in every import of a
  third-party plugin.

Two-way doors: the list of first-party plugins, the bridges, Sigstore later,
the size limits.

## Where it fits in the roadmap

A proposal only; this RFC does not edit `docs/roadmap.md`.

- **A new milestone, "Sweep plugins", after milestone 9 and before the
  agents milestone.** It takes the number 10, and the agents milestone moves
  to 11. It
  needs runners and the runner protocol (milestone 5), the implementation of
  ADR 164 (the sweep, the upload and the match), the job proxy of RFC 16 in
  the runner, and the OIDC issuer of ADR 204.
- **Scope:** the contract and report version 2; the repository with the
  SDK and `sweepplugintest`; admission and the launcher checks; D4; the four
  first-party plugins. The two bridges are a second part of the same
  milestone, or "Later".
- **Exit test (sketch):** on Linux, on PostgreSQL, through the real binary,
  with a pool that has both purposes: a signed Kubernetes plugin sweeps a
  kind cluster with 3 resources in no unit; pass: 3 findings, coverage
  `swept` for each kind, `not-swept` for Secrets with the `view` role; a
  plugin with a changed byte fails before it starts, with an audit entry; the
  plugin cannot reach the instance metadata address; a report that marks a
  type `unsupported` resolves no finding of that type.

## Open questions for the owner

1. **A new repository?** Should sweep plugins live in a new repository,
   `OpenDustpan/sweep-plugins`, as a second exception to ADR 343 decision 6?
   The alternative is the provider kit repository.
   _ELI5: we want a separate toy box where anyone can add a new toy without
   opening the main box. The rule says only one toy has its own box today.
   May this one have a box too?_
2. **A process contract or gRPC?** Should a sweep plugin be a program that
   reads a file and writes a file (`describe`, `sweep`), not a gRPC service?
   _ELI5: we can talk to the helper on the phone the whole time, or give it
   a note and get a list back. It only ever makes one list, so the note is
   simpler and anyone can write one._
3. **Credentials on a pool with two purposes.** On a pool that also applies
   changes, should a sweep get only a short-lived token for that one sweep
   (OIDC, through a profile), and never the identity of the host and never a
   stored key?
   _ELI5: the robot that builds things has a master key. When it only
   counts things, we give it a visitor badge for one hour instead, and take
   the master key away._
4. **The AWS gap.** AWS signs each request in the client, so the plugin sees
   a read-only credential of at most one hour. Is that acceptable with the
   limits named, or should AWS sweeps run only on a pool with the sweep
   purpose only?
   _ELI5: for one shop, the visitor must hold the badge in their own hand.
   They could show it to a friend for an hour. Do we allow that, or send a
   different robot that never builds things?_
5. **Who may sign.** Should a plugin run only with the signature of the
   project key, or also with a key that the operator adds for its own or a
   third party's plugins? A plugin with no signature never runs.
   _ELI5: only toys with our sticker, or also toys with a sticker that your
   parents trust? Toys with no sticker never come in._
6. **Resolve only what was swept.** Should a finding resolve only when the
   plugin says it swept that type? This changes how version 1 reports
   behave only if they move to version 2.
   _ELI5: if you looked only under the bed, you may not say that the closet
   is clean._
7. **A key for every provider.** Should the resource index get a key of the
   form tool, provider, type and import ID, so that sweeps can match
   resources of any provider in the registries, not only AWS, Azure and
   Google Cloud? It cannot change easily later.
   _ELI5: today we can only recognize three kinds of name tags. A general
   name tag lets us recognize every kind, but once we print them, we keep
   them._
8. **The bridges in the first slice?** Should the generic bridges to
   OpenTofu providers and Pulumi providers come in the same milestone as the
   four first-party plugins, or later?
   _ELI5: first we build four helpers that know the big clouds. Later a
   translator lets every provider help. Do we build the translator now or
   next?_

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write waits
  for another installation (ADR 248 decisions 3, 6 and 9). Targets, sweeps and
  findings stay namespace data. The admission of a plugin is installation
  data, as a trusted key is; a namespace on another installation needs the
  plugin admitted there too.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
