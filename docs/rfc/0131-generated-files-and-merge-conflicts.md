---
rfc: 131
title: Generated files and merge conflicts
status: draft
created: 2026-11-15
---

# RFC 131. Generated files and merge conflicts

Status: draft. The project owner asked on 15 November 2026 how other projects
handle merge conflicts in generated files. This RFC gives the facts, the
options and a recommendation. It changes no decision. ADR 275 decision 2, ADR
323 decision 3 and the definition of done in `CLAUDE.md` (`make check` passes
on each pull request) stay as they are until the owner decides.

## Problem

Pull requests that change the API description conflict in generated files. A
recent example is the pull request that names the collection of a placement
rule by its stable ID: it conflicted in `clients/typescript/src/description.ts`
and `metadata.ts`.

What the tree holds today (measured on `main`, 15 November 2026):

| File | Size | Lines | Commits that changed it since 1 November |
| --- | --- | --- | --- |
| `clients/typescript/src/description.ts` | 538 KB | 7 | 39 |
| `clients/typescript/src/metadata.ts` | 1 KB | 29 | not counted |
| `clients/typescript/src/operations.ts` | 168 KB | 5916 | not counted |
| `internal/api/apidesc/openapi.json` | 483 KB | line per field | 43 |
| `internal/clientgen/golang/testdata/golden/*` | - | line based | 31 |

`main` took about 790 commits in the same period.

Two facts matter:

1. `description.ts` holds the whole description as one string literal on one
   line. It also holds the digest of that string in `metadata.ts`. Git merges
   by line. Two pull requests that both change the description change the same
   line of `description.ts` and the same line of `metadata.ts`. These two files
   conflict every time. The line-based files (`operations.ts`, `types.ts`, the
   golden files, `openapi.json`) merge most of the time, and conflict only when
   two pull requests change adjacent lines.
2. The repository already has a bot for the conflicts that remain
   (`auto-regen.yml`, `auto-regen-resolve.yml`, `auto-regen-push.yml`,
   `scripts/auto-regen.sh`). It finds a pull request that conflicts only in
   generated paths, merges `main`, takes the side of `main`, runs the
   generators and pushes the result. For the pull request above it needed three
   resolve runs (two were canceled by the next push to `main`) and about 10
   minutes. Each push to `main` can put the next pull request in the queue
   back into conflict.

`.gitattributes` does not exist. No path is marked `linguist-generated`, so the
generated files show in full in each review diff.

## What others do

None of the projects below resolves a conflict in a generated file inside the
merge step. Three groups of approach exist.

### A. Commit the output, check it in CI, the author regenerates

| Project | What it does | Source |
| --- | --- | --- |
| Kubernetes | Output is committed. `hack/verify-codegen.sh` fails when `hack/update-codegen.sh` gives a different tree. The contribution guide says to keep the generated files in a separate commit from the edited `types.go`. Generated paths have `linguist-generated=true`. No merge driver is set for them. A Prow label `needs-rebase` flags a conflict. It does not fix it. | [contribute-upstream](https://kubernetes.io/docs/contribute/generate-ref-docs/contribute-upstream/), [verify-codegen.sh](https://raw.githubusercontent.com/kubernetes/kubernetes/master/hack/verify-codegen.sh), [verify-generated.sh](https://raw.githubusercontent.com/kubernetes/kubernetes/master/hack/lib/verify-generated.sh), [.gitattributes](https://raw.githubusercontent.com/kubernetes/kubernetes/master/.gitattributes), [labels](https://raw.githubusercontent.com/kubernetes/test-infra/master/label_sync/labels.md) |
| terraform-provider-aws | Output is committed. `make gen` makes it and the check `gen-check` fails a stale tree. | [makefile cheat sheet](https://hashicorp.github.io/terraform-provider-aws/makefile-cheat-sheet/) |
| pulumi-aws | "You must generate and check in the SDKs on each pull request containing a code change." `sdk/**/*` has `linguist-generated=true`. A bot (`upgrade-provider`) opens the upgrade pull requests with regenerated SDKs. | [CONTRIBUTING](https://raw.githubusercontent.com/pulumi/pulumi-aws/master/CONTRIBUTING.md), [.gitattributes](https://raw.githubusercontent.com/pulumi/pulumi-aws/master/.gitattributes), [upgrade-provider](https://raw.githubusercontent.com/pulumi/upgrade-provider/main/README.md) |
| grpc-go | Output is committed. `scripts/regenerate.sh` rewrites it. (A CI check was not verified.) | [regenerate.sh](https://raw.githubusercontent.com/grpc/grpc-go/master/scripts/regenerate.sh) |
| sqlc | `sqlc diff` compares the output on disk with a fresh run. The docs do not say where to commit it. | [CI/CD](https://docs.sqlc.dev/en/latest/howto/ci-cd.html) |

This is the most common pattern, and it is what this repository does now. The
repository adds a bot that does the regeneration after a conflict. The
projects above do not show one for conflicts. Only a bot for upgrades.

### B. One owner regenerates, contributors do not edit the output

| Project | What it does | Source |
| --- | --- | --- |
| Stripe SDKs | Output is committed in each SDK repository and made by a private generator. A pull request that touches generated code "will likely need" a change in the generator first. | [stripe-go](https://raw.githubusercontent.com/stripe/stripe-go/master/CONTRIBUTING.md), [stripe-node](https://raw.githubusercontent.com/stripe/stripe-node/master/CONTRIBUTING.md) |
| Speakeasy | The generator does a three-way merge of its new output, the old pristine output and the custom code. On overlap it writes conflict markers. | [custom code](https://www.speakeasy.com/docs/sdks/customize/code/custom-code/custom-code-reference) |
| Fern | `.fernignore` protects files you own. A three-way merge ("Replay") reapplies your edits to each new output and reports a conflict in the pull request body. | [custom code](https://buildwithfern.com/learn/sdks/overview/custom-code) |
| Stainless | A syntax-aware merge resolves "repetitive, low-risk" conflicts. | [changelog](https://www.stainless.com/changelog/semantic-custom-code-conflict-resolution) |

These vendors solve a different problem (custom code next to generated code in
a client repository). They show that a generator-aware merge is the only kind
that works. They do not apply to the API description of one repository.

### C. Output is not in the main repository

| Project | What it does | Source |
| --- | --- | --- |
| Buf Schema Registry | The registry builds the SDK. A consumer installs a package and has no checked-in stubs. | [generated SDKs](https://buf.build/docs/bsr/generated-sdks/) |
| pulumi-azure-native | The Go SDK is published to its own repository, "to remove the need to commit the SDK code into this repository". | [CONTRIBUTING](https://raw.githubusercontent.com/pulumi/pulumi-azure-native/master/CONTRIBUTING.md) |
| Kubernetes staging | `client-go` is published from `staging/` of the main repository to separate repositories. The published repositories take no pull request. | [staging README](https://raw.githubusercontent.com/kubernetes/kubernetes/master/staging/README.md) |
| Rust `build.rs` | Output goes only to `OUT_DIR`. Nothing is committed. | [build scripts](https://doc.rust-lang.org/cargo/reference/build-scripts.html) |

Go libraries commit generated code, because a consumer needs it in the
repository ([Go blog](https://go.dev/blog/generate)).

### D. Remove the conflict by structure

HashiCorp avoids changelog conflicts: each pull request adds its own file
`.changelog/<number>.txt`, and a tool joins them. "Because each PR or unit of
change has its own file, you should never encounter merge conflicts for
changelog entries."
([changelog process](https://raw.githubusercontent.com/hashicorp/terraform-provider-aws/main/docs/changelog-process.md),
[go-changelog](https://raw.githubusercontent.com/hashicorp/go-changelog/main/README.md))

### What does not work

- **`linguist-generated`** changes only the language statistics and the default
  diff view. It does nothing for a merge
  ([GitHub docs](https://docs.github.com/en/repositories/working-with-files/managing-files/customizing-how-changed-files-appear-on-github)).
  It is still worth setting, for the review diff.
- **A merge driver** (`merge=union`, `merge=ours`, or a driver that runs a
  generator, as `npm-merge-driver` does for lock files) is defined in the
  Git configuration of each machine, not in the repository
  ([gitattributes](https://git-scm.com/docs/gitattributes),
  [npm-merge-driver](https://raw.githubusercontent.com/npm/npm-merge-driver/latest/README.md)).
  GitLab.com does not support custom drivers
  ([GitLab docs](https://docs.gitlab.com/user/project/repository/files/git_attributes/)).
  We found no GitHub document that says a server-side merge runs one
  (not verified). `union` can leave lines in a wrong order.
- **A merge queue that regenerates.** The GitHub merge queue tests a temporary
  `gh-readonly-queue/*` branch. A pull request that fails is removed
  ([merge queue docs](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)).
  GitLab merge trains drop a conflicting request
  ([merge trains](https://docs.gitlab.com/ci/pipelines/merge_trains/)).
  Mergify, Kodiak and Aviator document no generator step
  ([Mergify](https://docs.mergify.com/merge-queue/),
  [Kodiak](https://kodiakhq.com/docs/config-reference),
  [Aviator](https://docs.aviator.co/mergequeue/concepts/fast-forwarding)).
  We found no project that regenerates inside the queue.

## Options

1. **Do nothing more.** Keep the committed output, `make clients-check`, the
   bot and the rule "run the generator again". Cost: each description change
   conflicts in `description.ts` and `metadata.ts` and waits for a bot run of
   about 10 minutes. The wait grows with the merge rate, because each push to
   `main` can send the next pull request back.
2. **Remove the guaranteed conflicts by structure (group D).** Change the
   generator so that two unrelated description changes do not touch the same
   line:
   - Write the embedded description as many lines (one line of text for each
     line of the description), not as one string literal. Git then merges two
     changes in different places of the description. Or write it as a
     separate file `description.json` that the package reads. This needs a
     design check against the rule that the default transport uses only
     `fetch` and `JSON`.
   - Take the digest and the client version out of the committed files. The
     publish step writes them (the first publish fixes the package, ADR 275).
     The committed tree then has no line that every description change must
     edit.
   - Add `.gitattributes` with `linguist-generated=true` for the generated
     paths, so that the review diff hides them.
   Cost: a change of the generator and of the template, and a review of the
   consumer pin (console ADR 5 uses the digest). It keeps the definition of
   done. Result: the remaining conflicts are the rare ones of line-based files,
   and the bot already handles those.
3. **Make the bot faster (group A with a bot).** Keep the files as they are.
   Run the resolve step when `main` moves for each pull request that is in the
   queue or that has auto-merge, and do not cancel a run that the next push
   would make useless (run once for the newest `main`). Cost: Actions minutes
   and more bot pushes. It does not remove the conflicts. Only the wait.
4. **Pull requests do not commit the generated clients (group C).** Only the
   train, or a bot after the merge, commits `clients/` and the golden files.
   This was option 2 of section 4c of RFC 120. It removes the conflicts. It
   also changes the definition of done in `CLAUDE.md`, a reviewer no longer
   sees the client change in the pull request, `main` can briefly hold a
   description that its client does not match, and a push of the bot to `main`
   needs an exception to the merge queue. It needs an ADR change (ADR 275
   decision 2 and ADR 323 decision 3).
5. **Publish only, no committed client (group C).** Generate the TypeScript
   client in CI and publish it. Delete `clients/typescript/`. The test of the
   package builds it from the description. This is the model of the Buf
   Schema Registry. The cost is the same as option 4, and the console also
   cannot read the client from a checkout of the server. ADR 275 decision 5
   already treats the published version as the "released" behavior.
6. **A merge driver.** Not recommended. The driver is not in the repository,
   and a server-side merge does not run it.

## Proposal

Option 2, then option 3 only if the measured wait stays a problem. Reasons:

- The two files that always conflict conflict because of their layout, not
  because of the number of pull requests. The projects in group D remove a
  conflict class by structure, and the project rule is to prefer a structure
  that makes a bug class impossible over a new check.
- No project in the research regenerates inside a merge step, so options 4 and
  5 buy the removal of conflicts at the price of review visibility and of a
  change to the definition of done. They are the owner's to take, not the
  default.
- Option 2 needs no owner decision on ADR 275 decision 2. It changes how the
  files are written, not whether `make check` passes on each pull request. The
  digest and version change in the first step needs a check of ADR 275 decision
  3 (the pin) and of console ADR 5 before the work starts.

Before any change, measure: for the last 100 merged pull requests that touched
the description, count the conflicts, by file, and the minutes from the first
conflict to the merge. Option 2 is justified when `description.ts` and
`metadata.ts` are most of them.

## Open questions

1. Who needs the digest in the committed `metadata.ts`? Console ADR 5 pins to
   it. Can the publish step write it, or must a checkout hold it?
2. Does a many-line `description.ts` keep the byte-exact digest rule? The digest
   covers the bytes of the description, not of the TypeScript file.
3. Does the owner want to take option 4 or 5 now, or only after the first tag?
4. Does the bot take an event for "the pull request is in the queue" (it has
   none today), or only the push to `main` and a timer?

## Checklist

- [x] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). This RFC changes how generated files are made in the repository. It changes no write path of the server.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
