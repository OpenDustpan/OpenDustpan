---
version: 1
---

Issue: #1103.

## Problem

Developer runs the Pulumi CLI or OpenTofu and then configures machines with Ansible, Puppet or Chef. They now pass IPs, hostnames and secrets with scripts or raw state reads, which expose secrets. They need one supported read path for outputs only.

## Evidence

- No non-test code references the string cloud.terraform in pkg, internal or cmd. Proof: rg -n -F --glob '!*_test.go' 'cloud.terraform' pkg internal cmd -> 0 matches
- Issue keeps Puppet, Ansible and Chef facts out of the inventory. Proof: Out of scope (owner decision): We do not pull Puppet, Ansible or Chef facts into the OpenDustpan inventory.

## Fix boundary

- Research note on a general versioned tool-neutral outputs API with per-output permission and separate secret handling, including version by state version and relation to output-sharing actions on main
- Research on Ansible dynamic inventory and lookup plugins including what cloud.terraform already does through the TFE-compatible API
- Research on Puppet Hiera backend and Chef data source options
- Research on short-lived token and workload identity auth, caching, unreachable-server behavior, ship locations and e2e with real tools

Out of scope:

- Puppet, Ansible or Chef facts into the OpenDustpan inventory
- Plugin code, Hiera backend code, data source code or API changes
- Token revocation, cache on revoked grants, audit of output reads and rate limits, except as open questions for a later change

## Check

- Write the research note at NEW docs/research/config-mgmt-outputs.md covering the six issue points and the relation to the output-sharing actions on main. It is a docs file: it calls no route and uses no credential. Check: scenario research-note-lists-six-points: the file NEW docs/research/config-mgmt-outputs.md exists and lists the six issue points
- Run docs checks for the note Check: make check with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Test helper reads outputs for a principal in output-share tests. (`pkg/outputshare/outputsharetest/principal.go:61 (func readOutputs)`)
- Access suite checks that unit read-only does not grant output reads. (`pkg/access/access_test.go:1787 (func TestCustomRoleWithUnitReadOnlyDoesNotReadOutputs)`)
- Access suite checks built-in roles hold the output-sharing actions. (`pkg/access/access_test.go:1732 (func TestBuiltInRolesHoldTheOutputSharingActions)`)
- Dustpan suite covers inventory routing. (`pkg/dustpan/dustpan_test.go:199 (func TestInventoryRouting)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 250: Defense in depth for security code** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 5 (current), 8 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Never only one layer. Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Quote: "Never either/or. Check and encode and limit. A value that you checked is still encoded where it is used. A sandboxed process still gets input that you checked. "It is checked upstream" is never a reason to remove, skip or omit a check."
- Quote: "Check at the boundary: an allowlist of type, length, character set and range. Reject bad input. Never change it to make it valid."
- Quote: "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed. A layer without its own test counts as missing."
- Quote: "Review rule. For a security-relevant change, the review lists each layer and its test. A missing layer, a missing layer test or a redundant check without its comment is a blocking finding, never a nit."
- Effect: The note carries the layers forward for the later API change: separate read action per output scope, secrets excluded by default, boundary allowlists, escape and limit at each client, one hostile test per layer.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Where do the Ansible, Puppet and Chef plugins live and ship?
   - Options: Separate repositories per tool registry; One repository under OpenDustpan with per-tool packages.
   - Provisional decision (owner review 14 Nov): Separate repositories per tool registry
   - Reason: After a step back, separate repos match how each tool installs content and keep release notes clear. Benchmark: AWS practice of least-privilege service-linked reads with short-lived credentials and no secret logging
   - Persona impact: Developer reads one note either way; choice changes only where they fetch the plugin. Escape hatch: Note lists a manual export path until plugins ship; no persona is blocked.

Questions considered and answered:

- Can Ansible, Puppet or Chef read secrets from inventory output? No. Secrets never appear in inventory output by default and need a separate read action per output scope, per ADR 250 decisions 1 and 2.
- How are units named in a general outputs API? Tool-neutral references of tool plus namespace plus the tool own name, versioned by state version. The note compares this with the output-sharing actions on main.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
