# 383. Generated files do not conflict by layout, and the resolve run does not cancel

Status: accepted · 2026-11-15. Decided by the project owner, who accepted the first and the third point of the proposal of RFC 131 ("both") on 15 November 2026

Changes:

- Extends ADR 275 decision 3: the digest and the client version are not in the committed files of the TypeScript package. The publish step writes them. The pin of a consumer stays the digest and the version of the published package.
- Extends ADR 323 decision 3: the layout of the generated files and the bot that resolves their conflicts. The rule "run the generator again" stays.

## Context

[RFC 131](../rfc/0131-generated-files-and-merge-conflicts.md) found that
`clients/typescript/src/description.ts` holds the whole API description as one
string on one line, and that `metadata.ts` holds the digest of that string.
Git merges by line, so two pull requests that change the description always
conflict in these two files. The bot of the repository
(`auto-regen-resolve.yml`) resolves such a conflict, but a newer push to
`main` cancels its run, so a busy period starves it. A resolve run for one
pull request needed three starts and about 10 minutes.

RFC 131 compared six options. No project in its research resolves a conflict
in a generated file inside the merge step. The owner chose the layout change
and the bot fix, and did not choose the measurement or a faster bot.

## Decision

1. **The generated files have no line that every change of the description
   must edit (owner, 2026-11-15).** The generator writes the embedded
   description as many lines, or as a separate file that the package reads.
   The digest and the client version are not in a committed file. The publish
   step writes them. The design check of the digest and of the transport rule
   (the default transport uses the global `fetch` and `JSON` only) is part of
   the change.
2. **The repository has a `.gitattributes` file (owner, 2026-11-15).** It
   marks the generated paths `linguist-generated=true`, so that the review
   diff hides them. It sets no merge driver, because a merge driver is in the
   Git configuration of a machine and a server-side merge does not run it.
3. **The resolve run of the bot is not canceled by a newer push to `main`
   (owner, 2026-11-15).** The run uses the newest `main` and a newer push
   starts one more run after it. The bot never leaves a pull request that it
   found with no run.
4. **The definition of done does not change (owner, 2026-11-15).** A pull
   request commits its generated files, and `make check` passes on each pull
   request. ADR 275 decision 2 and ADR 323 decision 3 hold. A conflict in a
   generated file is still resolved by a new run of its generator.
5. **A tooling change has no OpenSpec change (owner, 2026-11-15).** The
   lanes start from this ADR and RFC 131.

## Consequences

- RFC 131 is accepted with this ADR. The proposal to measure the conflicts
  and the proposal of a faster bot are not taken.
- The change of decision 1 touches the TypeScript template set and the
  package check, `make clients-check`. It changes the golden files of
  `internal/clientgen/typescript`. It needs a check of the pin of the console
  (console ADR 5) before it starts.
- The change of decision 3 touches `auto-regen.yml`,
  `auto-regen-resolve.yml` and `scripts/auto-regen.sh`. A workflow file goes
  through the workflow patch process.
- Removing the commit of the clients from pull requests, and a package with no
  committed client, stay options of RFC 131 and are not decided here.
