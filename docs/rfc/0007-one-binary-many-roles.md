---
rfc: 7
title: One binary, many roles, removable features
status: draft
created: 2026-10-10
---

# RFC 7. One binary, many roles, removable features

Status: draft.

## Problem

Operators range from one developer on a laptop to an enterprise with a DMZ.
They ask for three things that pull in different directions:

1. **Turn features off.** A team that does not want a feature should be able
   to switch it off with one setting, and its endpoints, console views and
   background work go away.
2. **Remove features from the binary.** A team with strict reviews wants code
   that it does not use to be absent, not only switched off. A Go build tag
   should remove a feature.
3. **Scale and place parts separately.** An enterprise wants to run the
   artifact registry (RFC 10) on more replicas than the API, and to put an
   upstream proxy with curation outside its DMZ, away from the state backend.

The obvious answer to 3 is microservices. Microservices also cost the most:
network APIs between services become contracts, each hop needs auth, and the
solo operator must run five processes instead of one.

## Proposal: a modular monolith with roles

This follows the Temporal server, which ADR 2 already uses as a model. The
Temporal server is one binary with four services (frontend, history,
matching, worker). By default one process runs all of them. In production
each runs as its own deployment with `--service`.

- **Features are modules.** Each feature (state for each tool, profiles,
  runners, backups, the artifact registry, the upstream proxy) is a module
  that registers itself with the server through an SDK option: its routes,
  its background work, its migrations and its capabilities.
- **Roles are sets of modules in one process.** `opendustpan serve --role
  api,registry` runs those modules. The default is `all`, so the solo
  operator still runs one process.
- **Runtime switch.** A module that is built in but not enabled registers
  nothing. The server lists the enabled modules at `/api/capabilities`, and
  the console hides the views of a module that is off.
- **Build tags.** `cmd/opendustpan` wires each module in its own file, behind
  a build tag (`//go:build !no_registry`). `go build -tags no_registry`
  leaves the module and its dependencies out of the binary. The SDK packages
  stay importable for anyone who writes their own `main` (ADR 2).
- **Shared storage, not shared calls.** Roles that need the same data share
  the database and the blob store, as Temporal services share their
  persistence. A role calls another role over the network only where the
  network is the point: the upstream proxy in the DMZ.

## Where a network boundary is real

- **Upstream proxy.** It fetches from the internet and curates. It runs in
  the DMZ, and the internal registry pulls from it. Data goes one way, and
  the proxy has no access to the state database. This is a separate role
  with its own small API and its own credentials.
- **Runners** (RFC 6) are already outside the server and talk to it over the
  API.
- **Backups** (RFC 8) can run as a role with read access only.

Everything else scales by adding replicas of the same binary with different
`--role` values.

## Options

1. **Modular monolith with roles** (above).
2. **Microservices in separate repositories or binaries.** This gives the
   most independence, at the cost above. It also breaks "graceful
   degradation from solo to enterprise".
3. **Runtime switches only, no build tags.** It is simpler, but it fails
   request 2.
4. **Build tags only.** Every change of features needs a new build, which
   fails request 1.

## Open questions

- Which modules exist on day one, and which belong to the core that cannot
  be switched off (namespaces, identity, fencing)?
- Do modules own their migrations? A module that is off must not leave
  half-migrated tables. A module that is removed by a build tag must not
  break `sqlc diff` or the conformance suites.
- How does the console learn module versions, so that an old console does
  not call a module that a newer server removed?
- Does `opendustpan serve --role` replace `serve`, or sit beside it?

## Outcome

Accepted in part as [ADR 211](../adr/0211-optional-modules-and-the-capabilities-document.md)
on 2026-10-18, as the project manager decided under the owner's decision rule: the core that is never off, one declared switch
for each optional module, 404 and no schedules for an off module, every
migration always applies, and the capabilities document. The roles of the
binary and `opendustpan serve --role` stay open.
