# OpenSpec project context: OpenDustpan

OpenDustpan is an open-source, self-hostable backend for infrastructure-as-code
tools: state, history, locks and secrets behind one set of namespaces,
identities and storage. The Pulumi CLI connects through a Pulumi
Cloud-compatible API. OpenTofu connects through a TFE-compatible API. More tools
come later. OpenDustpan is not affiliated with any tool's vendor.

`CLAUDE.md` (the same file as `AGENTS.md`) holds the rules for code. This file
holds the rules for specs. Where the two overlap, `CLAUDE.md` wins.

## Personas

A proposal starts from one of these people.

| Persona | What they try to do |
| --- | --- |
| Operator | Install, configure, back up, restore and upgrade a server. Set the installation settings. |
| Site administrator | Manage sign-in providers, principals, groups, grants and the audit log of an installation. |
| Namespace administrator | Manage the units, members and settings of one namespace. |
| Developer | Run the Pulumi CLI or OpenTofu against the server, read history, and approve or review changes. |
| Runner operator | Run a runner pool that executes jobs. |
| Agent | A program that uses the REST API with an access token. |

Add a persona here when a proposal needs one that is not in this table.

## Conventions

Use the terms of `GLOSSARY.md`. Avoid the words it lists under _Avoid_. Write
English in Simplified Technical English (`asd-ste100` skill) with US spelling.
Names, voice, tool labels ("Pulumi", "OpenTofu") and the "unit" rule are in
`CLAUDE.md`. The main points for a spec:

- The tool-compatible APIs are contracts. A change to them is checked with
  `make e2e` against the real CLI.
- Every feature has a REST API. The console, the CLI and other clients use only
  that API.
- Every endpoint declares its authorization actions and the object of each
  action. Every permission is a stored grant.
- Each automatic action writes a record of who or what did it and why. Each
  default shows its effective value in the API.
- Security code uses defense in depth. A spec for it lists each layer and the
  hostile test of each layer.
- The schema has one baseline migration per dialect until the first tag. A spec
  never adds a numbered migration or a compatibility shim.
- A stored reference to an entity that a user can rename, or remove and make
  again, uses the stable ID. A name is a label for people only.
- Do not add a dependency, and do not add phone-home code, without the owner.

## The ADR rule

ADRs are decisions. RFCs are open questions. Read `docs/adr/INDEX.md` before you
write a proposal. A proposal never contradicts an accepted ADR. If it does, write
an RFC first. Do not write an ADR until the project owner accepts the decision.
A later ADR can change an earlier one: the "Current rule" column of the index
names the ADRs to read.

## How a change is made

A change lives in `openspec/changes/<change-name>/`. It has `proposal.md`,
`design.md`, `tasks.md` and one delta spec for each capability it touches in
`specs/<capability>/spec.md`. Agent work starts from a change that has no open
questions. The pull request links the change and its issue.

### Proposal

1. **Start from a person.** The "Why" names a persona and what they try to do.
   It walks the user path through the surfaces the change touches: the API, the
   CLI, the docs page and the console. For each step it gives the errors the
   person may hit and the one command that helps. Errors say what failed and
   name that one command (see "Voice" in `CLAUDE.md`).
2. **ADR check.** Every proposal has a section "ADR check". It lists each ADR
   that the change touches, and for each ADR:
   - the quoted lines of the decisions that apply;
   - whether the decision is owner-tagged. A decision is owner-tagged when its
     text carries "(owner)" or "(owner, ADR N)", or when the title of the ADR
     carries "(owner)";
   - the "Changed by" chain of the ADR, followed to the end, with the "Current
     rule" of `docs/adr/INDEX.md`.

   A conflict with an owner-tagged decision blocks the change until the owner
   decides. A conflict with another ADR needs an RFC first.
3. **Open questions.** Every proposal has a section "Open questions". A change
   may be implemented only when that section reads "none" (the spec-ready
   gate). Each open question names who decides it.
4. **Short form.** A bug or a tech-debt change may use a short proposal:
   problem, evidence, fix boundary, check and "Open questions: none". It still
   has an ADR check, but it can be one line when no ADR is touched.

### Design

`design.md` prefers a structure that makes a bug class impossible over added
checks. If a design adds a check, it says why no structure removes the need for
it. A design does not add new tooling as a fix.

### Tasks

Each task names its acceptance check: a test, a command or an API call that
shows the task is done. The definition of done for a change is:

- the wiring: the feature is reachable from the server, and not only built;
- the route, with its authorization actions;
- an e2e step against the real CLI or the real directory, where the change has
  a user-visible path;
- the docs page, in the same pull request, for a feature that a user sees;
- a usability check: the persona completes the user path from the docs page
  alone, and each error names the one command that helps;
- `make check` with zero findings.

### Validation

Run `npx @fission-ai/openspec@1.15.0 validate --all` and `make docs-lint`.
Set `DISABLE_TELEMETRY=1`, `OPENSPEC_TELEMETRY=0` and `DO_NOT_TRACK=1` when you
run the tool. A version change follows the rule for tools: the release must be
at least two weeks old.

The slash commands and skills for Claude Code and OpenCode are plain files in
`.claude/` and `.opencode/`. They were generated by `openspec init` and
reviewed. They call the `openspec` command. Run the pinned version through
`npx @fission-ai/openspec@1.15.0` or install it with the same version.
