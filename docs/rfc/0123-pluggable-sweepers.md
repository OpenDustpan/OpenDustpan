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
4. **A generic sweep plugin for OpenTofu providers**, which starts a
   provider binary and calls the `ListResource` call of the provider plugin
   protocol, with resource identity as the match key. It uses only
   MPL-2.0 protocol files. Its value depends on the provider support that
   section "Facts" gives.
5. **For Pulumi**, a convention for a provider function with a fixed name
   and shape, which a provider can adopt natively, and the provider kit
   (ADR 170) implements it first.
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
| RFC 16 | The job proxy, with request rules and credentials that only the proxy holds. AWS Signature Version 4 is out of its scope. | Option D must treat AWS apart. |
| RFC 93, ADR 269, ADR 349 | The vetted supply chain; private providers are uploaded as the archive set (zip files, `SHA256SUMS`, its signature); the OpenPGP library is allowed in the server registry. | A sweep plugin release can reuse the archive set and its check. |
| ADR 204 | OpenDustpan is an OIDC issuer for profiles. Whether a sweep gets a token stays open. | Option D3. |
| ADR 250 | Defense in depth for every input from outside the process. | Section "Security". |
| ADR 347 | Warn, do not block, for choices that the operator owns. Security controls keep their own rules. | The signature check and the sandbox are security controls. |

## Facts from primary sources

RESEARCH-PLACEHOLDER

## The product manager's sketch, tested

SKETCH-PLACEHOLDER

## Options

OPTIONS-PLACEHOLDER
