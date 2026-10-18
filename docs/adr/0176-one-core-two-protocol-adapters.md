# 176. One core, two protocol adapters

Status: accepted · 2026-10-18 · part of ADR 170

## Context

The project owner decided on 17 October 2026 that one description produces
an OpenTofu provider and a Pulumi provider (owner). The owner left the method
open: a provider plugin protocol server, a Pulumi bridge-like layer of our
own, or two thin adapters over one core.

Each tool starts a provider as a plugin process and talks to it over gRPC:

- OpenTofu starts the plugin, reads a handshake line from its standard
  output, and speaks the provider protocol. Version 6 of that protocol has
  the calls for schemas, configuration, plan, apply, read, import and data
  sources.
- The Pulumi CLI starts a plugin named `pulumi-resource-<name>`, reads a
  port from its standard output, and speaks the resource provider protocol:
  schema, check, diff, create, read, update, delete, and invoke for
  functions.

The options:

1. **An OpenTofu provider only, and the Pulumi bridge of the Pulumi project
   for Pulumi users.** The Pulumi CLI can wrap a provider of the other
   protocol at run time. It is the least code for us. But Pulumi users then
   depend on a layer that another project owns, with an extra process and a
   schema translation that we do not control. The secret and write-only rules
   of ADR 175 would then depend on that layer.
2. **A bridge-like layer of our own.** We would build the OpenTofu provider,
   and translate its protocol to the Pulumi protocol. That is two hops, and
   the concepts of one tool leak into the other.
3. **Two thin adapters over one core.** Each adapter speaks the protocol of
   one tool and calls the core port of ADR 172. Neither adapter knows the
   other.

## Decision

1. **Two thin adapters over one core (option 3).** `protocol/tofu` serves the
   OpenTofu provider protocol, version 6. `protocol/pulumi` serves the Pulumi
   resource provider protocol. Each translates its calls to
   `lifecycle.Provider` and calls `lifecycle.Plan` for every change. The two
   tools then show the same change for the same declaration. This follows
   ADR 12: a shared core, one adapter for each tool.
2. **One binary, two names.** A build makes one binary. It reads the base
   name of its program, as the launcher does (ADR 134 decision 2). A name
   that starts with `terraform-provider-<name>_v` serves OpenTofu: the
   archive holds the binary as `terraform-provider-<name>_v<version>`
   (ADR 177). The name `pulumi-resource-<name>` serves the Pulumi CLI. Any
   other name stops the binary with an error that names the two forms. The
   release packs the same binary under each name.
3. **Names in each tool.** For a provider `ipam` with a resource
   `allocation` and a data source `prefix`:
   - OpenTofu: the resource type `ipam_allocation` and the data source
     `ipam_prefix`.
   - Pulumi: the resource `ipam:index:Allocation` and the function
     `ipam:index:getPrefix`. Property names are camelCase, for example
     `prefixId`.
4. **Types.** `string`, `bool`, `list` and `map` map directly. A `number`
   is a number of arbitrary precision in OpenTofu, and a 64-bit float in the
   Pulumi protocol. A number with more than 53 bits of precision loses
   precision in Pulumi. The generated documentation says so.
5. **Pulumi SDKs.** The Pulumi adapter returns a full package schema. A
   Pulumi user runs `pulumi package add` with the plugin to get a local SDK
   in the language of the program. OpenDustpan publishes no SDK for each
   language.
6. **The handshake without the `go-plugin` library.** OpenTofu starts a
   provider with the plugin protocol of the `go-plugin` library (MPL-2.0).
   The adapter implements the parts that OpenTofu uses, on `net`,
   `crypto/tls` and gRPC, without the library:
   - The magic cookie. OpenTofu sets the variable `TF_PLUGIN_MAGIC_COOKIE`
     to a fixed value. Without it, the binary stops with an error that says
     it is a plugin and not a command for a person.
   - The protocol version. OpenTofu lists the versions that it speaks in
     `PLUGIN_PROTOCOL_VERSIONS`. The adapter picks 6, and stops with an
     error when the list does not have it.
   - AutoMTLS. OpenTofu gives its client certificate in
     `PLUGIN_CLIENT_CERT`. The adapter makes a self-signed server
     certificate for the run, accepts only that client certificate, and
     sends its own certificate in the handshake line.
   - The handshake line on the standard output:
     `1|6|tcp|127.0.0.1:<port>|grpc|<certificate>`.
   - The `GRPCController` service with `Shutdown`, which OpenTofu calls to
     stop the plugin. The adapter also serves the stdio and broker services
     when the e2e step shows that OpenTofu needs them.
7. **Each adapter lands with its e2e step.** An adapter is supported only
   once `make e2e` drives the real CLI through a provider that it serves
   (AGENTS.md). The e2e suite builds the example declarations of
   `providerkit/declaration/testdata` against a local fake API and a fake
   command.

## Consequences

- One rule change, for example in planning or in secrets, changes both tools
  at once.
- Two protocol adapters are two pieces of gRPC code to keep up with two
  protocols. A new protocol version of either tool is a change in one
  adapter only.
- A Pulumi user installs a plugin and generates a local SDK. That is one more
  step than a package from the Pulumi registry.

## Open questions

These are dependency questions for the project owner, with licenses.

- `google.golang.org/grpc` (Apache-2.0) and `google.golang.org/protobuf`
  (BSD-3-Clause). Both adapters need them.
- The protocol definition of OpenTofu, `tfplugin6.proto`, is in the OpenTofu
  repository under MPL-2.0. The adapter needs Go code generated from it.
  Copying the file or its generated code into this repository brings an
  MPL-2.0 file into an Apache-2.0 project. MPL-2.0 is file-level copyleft,
  and the license gate accepts it (`make trivy`), but the project owner
  decides.
- The `go-plugin` protocol definitions (`grpc_controller.proto`,
  `grpc_stdio.proto` and `grpc_broker.proto`) are in the `go-plugin`
  repository under MPL-2.0. The services of decision 6 need Go code
  generated from them, which has the same license question as
  `tfplugin6.proto`.
- The protocol definitions of the Pulumi CLI are in the Pulumi repository
  under Apache-2.0. The adapter needs Go code generated from them.
- `protoc` and `protoc-gen-go` are build tools, not linked code. The
  generated files go into version control, so a build needs neither tool.
