## Context

The tag workflow pushes the image and the chart. The archives have scripts (`scripts/release-build.sh`, `scripts/release-verify.sh`) and tests, but no workflow job calls them. The gap is the wiring: build, check, store, upload. The change adds the wiring with the least new code. The checks stay in the existing scripts, and one new script holds the one step that the workflow could not test: the upload.

## Decisions

1. **Job order: build, verify, image and chart, draft.** `archives-build` has no `needs`. `archives-verify` needs `archives-build`. `publish` (the image and the chart) needs `archives-verify`. `release-draft` needs `archives-verify` and `publish`. A bad archive stops the run before the first registry push. A registry push is public and cannot be taken back, so everything that can fail cheaply runs first. The draft is last: it is the one step that a person can undo. A draft in the release list means that the archives, the image and the chart are all out.
2. **The verify job reads the stored files.** `archives-build` stores `SHA256SUMS` and the four archives with `actions/upload-artifact`. `archives-verify` is a second job on a second runner. It downloads the files, runs `scripts/release-verify.sh`, and then runs `sha256sum --check --strict SHA256SUMS` as a second, independent comparison. So the job checks the bytes that travel to the upload, not the directory that the build wrote. `release-draft` downloads the files again, and `scripts/release-upload.sh` runs both checks once more before it calls `gh`. Three comparisons in three places, each on the files as they are at that point.
3. **One upload script, with the checks inside it.** `scripts/release-upload.sh` cannot create a release without passing the same checks, so "upload only after verify" holds by construction and not only by the order of jobs. A caller that skips the verify job still meets the check. The alternative, an inline `gh release create` in the workflow, would leave the upload untestable without a tag.
4. **A draft release.** The script calls `gh release create <tag> --draft --verify-tag`. A draft is visible to people with write access only. A maintainer reads the draft and publishes it with `gh release edit <tag> --draft=false`. The workflow never publishes. This follows "no tag and no release until the owner says so": the tag push is the owner's act, and the publish is a second act. A published release is the one result that cannot be undone without a public trace, so the workflow does not make it. If the owner later wants an automatic publish, that is one flag in the script and a new ADR.
5. **No overwrite, no retry, no delete.** The script lists the releases of the repository, drafts included (`gh api repos/<repo>/releases --paginate`), and refuses when one has the tag. It never calls `gh release upload --clobber` and never edits an existing release. A failed upload can leave a draft with some files. The script prints the delete command and exits non-zero. It does not delete the draft itself: a delete is a destructive act, and the maintainer who re-runs the job runs it. (UNVERIFIED: whether `gh release delete <tag>` finds a draft by tag on the `gh` version of the runner image. The test with a fake `gh` cannot show it. The maintainer section gives the fallback `gh api -X DELETE repos/<repo>/releases/<id>`, and the first real run shows which form works.)
6. **The chart stays out of this change.** The workflow packages and pushes the chart already, and ADR 246 decision 3 says the chart ships as an OCI artifact in the registry with no web index. A chart file on the release page would be a second copy to trust, and `SHA256SUMS` would have to list it, which `scripts/release-verify.sh` refuses (the directory holds exactly four archives and `SHA256SUMS`). So ADR 283 decision 3 holds for the chart through the existing step, and this change adds nothing for it. It changes the `publish` job only by `needs` and by its own `permissions` block.
7. **Least permissions, per job.** The workflow sets `permissions: {}` for the file. `archives-build` and `archives-verify` have `contents: read`. `publish` has `contents: read` and `packages: write`, which is what it has today. `release-draft` has `contents: write` and nothing else. No other job can write to a release. Every new checkout has `persist-credentials: false`, so the token does not stay in `.git/config` for a later step. `archives-build` and `archives-verify` read no secret.
8. **The version goes through the environment.** Each step that uses the tag reads `${{ github.ref_name }}` in an `env:` block and uses `"$RELEASE_VERSION"` in the script. No new `run:` block holds `${{ ... }}`. A tag name is chosen by the person who pushes it, so a name such as `v1$(id)` must reach a shell as data only. The scripts check it again with an allowlist (`scripts/release-build.sh:30`, `scripts/release-verify.sh:122` and the new script).
9. **Pinned actions, no new action.** The change adds no action that is not on main already: `actions/checkout`, `actions/setup-go`, `actions/upload-artifact` and `actions/download-artifact`, each with the SHA that another workflow of this repository pins today. Each pin is more than two weeks old, since main holds it already. A test refuses a pin that is not a full SHA with a release comment. `setup-go` runs with `cache: false`: a release build never restores a cache that a pull request could have written.
10. **Go toolchain.** `setup-go` reads `go.mod`, as `ci.yml` does, so the toolchain of `go.mod` builds the archives.
11. **Two pull requests, because of the workflow limit.** The cloud agent cannot push `.github/workflows`. The first pull request has the script, its tests, the docs and the Makefile line. It merges alone and green. The second has only the workflow patch and the workflow test, and the managed Droid computer `dustpan` opens it. It never merges it and never arms auto-merge. The workflow test fails on main until the patch is in, so it lives in the second pull request.
12. **A failed draft job.** A re-run of all jobs after a full release stops in `publish`: the chart step refuses a chart version that is released (`.github/workflows/release.yml:219-224`), so `release-draft` is skipped. The maintainer section says to use "Re-run failed jobs" for a failed `release-draft`. The change leaves the re-run rules of `publish` as they are.

## Relation to other changes

- `release-artifacts-image-install-docs` changes `.github/workflows/release.yml` too: its patch adds `--build-arg VERSION="${TAG}"` to the `docker buildx build` line (`.github/workflows/release.yml:161`). The two patches touch different hunks (that one a step of `publish`; this one the header comment, the `permissions` block, the `publish` job header and new jobs). Either order applies. The person who applies the second patch runs `patch --dry-run` first.
- That change adds the operator section "Install from a binary archive" to `docs/guides/install.md`. Its proposal says that the page makes no promise of an archive download until the upload exists, but its task 2.5 adds a download block. This change does not edit that section. It requires that the download block does not reach `main` before this change has an applied workflow: until then no release page file exists. Task 4.2 checks this and checks that the archive names of the section equal the names that `scripts/release-verify.sh` expects (`scripts/release-verify.sh:134-138`).
- `release-artifacts-binary-archives` owns the build and verify scripts. This change does not edit them.

## Contract

### `scripts/release-upload.sh <version> <dir>`

Input:

- Two arguments: the version, for example `v0.1.0`, and the directory that holds the set.
- The environment variable `GH_REPO`, `owner/name`, checked against `^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`. The environment variable `GH_TOKEN` is read by `gh` only. The script never reads, prints or passes it.

Steps, in this order. A failed step ends the script with a non-zero exit code, and no later step runs:

1. Two arguments, else `usage: release-upload.sh <version> <dir>`.
2. The version matches `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`.
3. `GH_REPO` is set and matches the allowlist.
4. `scripts/release-verify.sh "$version" "$dir"` passes.
5. `cd "$dir" && sha256sum --check --strict SHA256SUMS` passes.
6. No release of the repository has the tag. The script reads the tag names with `gh api "repos/$GH_REPO/releases" --paginate --jq '.[].tag_name'` and compares each with the version as a whole line (`grep -Fxq --`). A failed `gh api` call is a refusal, never an empty list.
7. `gh release create "$version" --draft --verify-tag --title "$version" --notes "$notes" -- SHA256SUMS opendustpan_<version>_linux_amd64.tar.gz opendustpan_<version>_linux_arm64.tar.gz opendustpan_<version>_darwin_amd64.tar.gz opendustpan_<version>_darwin_arm64.tar.gz`, run in `$dir`, with the five names written out and no glob. `$notes` is a fixed text: the archive names, the sentence "SHA256SUMS is not a signature.", and the check command `grep ' <archive>$' SHA256SUMS | sha256sum -c -`.

Output, exact:

- Success, one line on stdout: `release-upload: <version>: draft release created with four archives and SHA256SUMS; review it, then publish it with: gh release edit <version> --draft=false`
- A refusal, one line on stderr: `release-upload: <what failed>; <the one command that helps>`. The cases:
  - bad version: `run make release-verify VERSION=v0.1.0`
  - bad `GH_REPO`: `set GH_REPO to owner/name`
  - a set that fails a check: the message of `release-verify.sh`, which ends with `rebuild the set, then run make release-verify VERSION=<version>`; for the second tool, `SHA256SUMS does not match the files; rebuild the set, then run make release-verify VERSION=<version>`
  - a release exists: `a release for <version> exists; list releases with: gh release list --repo <repo>`
  - `gh` fails after the draft may exist: `the draft may exist with some files; delete it with: gh release delete <version> --repo <repo>, then re-run the failed job`

The script writes no file, runs no archive member, uses no `eval` and prints no environment variable.

## Layers (security code)

The supply chain of a release is security code: it decides what an operator runs. Each layer below stops its attack without the others. The hostile test of a layer sends bad input to that layer as if the layers before it had passed.

| Layer | Guard | Hostile test (NEW symbol unless it says "on main") |
| --- | --- | --- |
| Workflow guard | Each job has the guard: `push` event, `refs/tags/v`, the main repository. | `TestReleaseWorkflowCheckerRefusesWeakWorkflows`, a table of mutations of the file: a job with no guard, a guard without the repository clause, a `pull_request` trigger. |
| Job order | The `needs` chain: no push and no upload before the verify job. | The same table: `publish` without `needs`, `release-draft` without `needs: publish`, `archives-verify` without `needs: archives-build`. |
| Permissions | `permissions: {}` for the file; `contents: write` only in `release-draft`; `packages: write` only in `publish`. | The same table: a job without a block, `contents: write` in a second job, `contents: write` for the file. |
| Pins | Each `uses:` is a 40-hex SHA with a `# vX.Y.Z` comment. | The same table: a tag pin (`@v7`), a short SHA, a pin with no release comment. |
| Version as data | No `${{ }}` in a `run:` block; the tag reaches a script through `env:`. | The same table: a `run:` line with `${{ github.ref_name }}`. `TestReleaseUploadRefusesABadVersion`: `$(id)`, a backtick, `;`, a space, a newline, `-x`, `v1.2`, `v01.2.3`, `v1.2.3-rc1`, 65 characters. |
| Build script allowlist | `scripts/release-build.sh:30`. | `TestReleaseBuildRefusesABadVersion` (on main, `internal/releaserecord/release_archives_test.go:97`). |
| Verify script | `scripts/release-verify.sh`: exact file set, `SHA256SUMS` line form, checksums, archive entries, stamp. | `TestReleaseVerifyRefusesBadInput` (on main, `internal/releaserecord/release_verify_test.go:324`). |
| Second checksum tool | `sha256sum --check --strict SHA256SUMS` in the verify job and in the upload script. | `TestReleaseUploadRefusesAChangedSet`: one flipped byte in an archive, a `SHA256SUMS` line for the wrong file, a missing line, a repeated line, an extra file, a link in the directory. Each case makes zero `gh` calls. The test replaces `release-verify.sh` in the copied tree by a script that always passes, so the second tool stops the attack alone. |
| No overwrite | The release list check. | `TestReleaseUploadRefusesAnExistingRelease`: a draft with the tag, a published release with the tag, a list call that fails, a list that holds the tag among others. Each case makes no `create` call. A release `v0.1.01` in the list does not block `v0.1.0`. |
| Draft only | `--draft` and `--verify-tag`; no publish flag; no `--clobber`. | `TestReleaseUploadCreatesOneDraft`: the recorded calls hold one `release create` with `--draft`, `--verify-tag` and five file names, and no `--latest`, no `--clobber` and no `release upload` or `release edit` call. |
| Authorization at the point of use | The actor check in `release-draft`, before the download and the upload. | `TestReleaseWorkflowArchiveJobs` checks that the actor step comes before the upload step and has the text of the check in `publish`; the same table removes it. |
| Fail closed, no secret in the log | The script ends on the first error and never prints `GH_TOKEN`. | `TestReleaseUploadKeepsTheTokenOut`: the test sets `GH_TOKEN` to a marker, fails each step in turn, and checks that the marker is in no output and in no argument of a `gh` call. |

Each check that looks redundant keeps a comment that says it is a defense-in-depth layer and names the layers it does not rely on: the `release-verify.sh` call and the `sha256sum` call in `release-upload.sh`, the actor and tag steps of `release-draft`, and the `sha256sum` line of `archives-verify`. The patch has those comments.

Task 2.6 takes out each guard in turn and records the test that fails.

## Test path with no tag

No tag and no workflow run are needed. The tests are plain `go test` tests in `internal/releaserecord`. They run in the Go job of every pull request: `.github/workflows/ci.yml` passes no `-short` flag (`rg -n -- "-short" .github/workflows/ci.yml Makefile` prints nothing).

- `TestReleaseUploadCreatesOneDraft` builds the archives of a test version in a copy of the work tree (`copyWorkTree`, `extendRecord`, as `TestReleaseArchivesAreReproducible` does), runs `scripts/release-upload.sh` with a fake `gh` first in `PATH`, and reads the recorded calls. The fake `gh` appends its arguments to a file and answers `gh api` from a file that the test writes. It is a test fake, not product behavior.
- `TestReleaseWorkflowArchiveJobs` reads `.github/workflows/release.yml` through `os.Root`, parses it with `go.yaml.in/yaml/v3` (a direct requirement in `go.mod:15`, imported by a test already), and checks the layers of the table. `TestReleaseWorkflowCheckerRefusesWeakWorkflows` runs the same checker on mutated copies. The checker is one function that returns a list of findings, so the mutation table asks "does the checker find this" through the code that the real check uses.
- A local run: `go test ./internal/releaserecord -run TestReleaseUploadCreatesOneDraft -count=1`. It needs no docker, no token and no network after the module download.

The first real run of the workflow is the first tag. The change cannot remove that risk. It reduces it: the scripts and the shape of the workflow are tested on every pull request. No test shows, before the first tag, how the real `gh` behaves against the real API (the draft delete, the `--verify-tag` flag) or how the artifact moves between two runners. The maintainer section lists both as things to read in the first run.

## Makefile line

`make release-record-check VERSION=...` interpolates the value into a shell string (`Makefile:193`). The hunk passes it through the environment, as `release-build` does (`Makefile:204-206`). The variable is exported only in the mode that reads it.

```diff
--- a/Makefile
+++ b/Makefile
@@ -187,11 +187,14 @@ openspec-validate: ## Validate every OpenSpec change and spec (openspec validate
 # exist without one: the script against an empty record, a record of another
 # version, a record with a CR or a NUL byte, and a missing record. With
 # VERSION=vX.Y.Z on the command line it checks the record for that tag, as a
 # release does. A VERSION in the environment does not change the mode.
+ifeq ($(origin VERSION),command line)
+release-record-check: export RELEASE_VERSION := $(VERSION)
+endif
 release-record-check: ## Check the release record: the cases that need no tag, or VERSION=vX.Y.Z for a tag.
 ifeq ($(origin VERSION),command line)
-	./scripts/check-release-record.sh '$(VERSION)'
+	./scripts/check-release-record.sh "$$RELEASE_VERSION"
 else
 	go test ./internal/releaserecord -run 'TestCheckReleaseRecordRefuses|TestReleaseBuildChecksTheRecordFirst|TestReleaseRecordTargetMode' -count=1
 endif
 
```

If the fix of issue 3661 is on `main` when the work starts, the hunk is there already and task 2.4 only checks it. A hostile `VERSION` such as `v1'; touch /var/tmp/release-marker; '` must leave no marker file and must fail with the version message of the script. The session that wrote this change applied the hunk, ran `make release-record-check VERSION="v1'; touch /var/tmp/release-marker; '"`, saw `version v1'; touch /var/tmp/release-marker; ' is not vMAJOR.MINOR.PATCH` and no marker file, and reverted the hunk.

## Structure

The bug classes are "an upload that skipped the check", "a token that a later step can read" and "a tag name read as code". No Go type can remove them: the inputs are a tag name, a file set and a workflow file. The structure that comes closest:

- The upload and the check are one script, so no caller can upload without the check.
- The permissions are per job and the default for the file is empty, so a new job has no permission until a person writes one.
- The version reaches every script through the environment, so a template value never becomes shell text.

What remains is a guard with a mutation test (task 2.6): the workflow checker. It adds no tool: it is a Go test of a text file that the module can already parse.

## Workflow patch

This is the exact diff for `.github/workflows/release.yml` on main at `3443bb35d`. It applies with `patch -p1`. The session that wrote this change ran `patch -p1 --dry-run` and then ran `go test -tags e2e -run TestReleasePushesOneDigestToBothRegistries ./e2e` on the patched file, with no finding, and reverted the patch afterwards. It goes into the body of the workflow pull request under "Workflow patch (apply locally)".

```diff
--- a/.github/workflows/release.yml
+++ b/.github/workflows/release.yml
@@ -1,7 +1,10 @@
-# Release workflow: on a final version tag, build one multi-arch server
-# image and push the same digest to the forge container registry first and
-# then to Docker Hub, compare the digests, and push the Helm chart as an
-# OCI artifact to the forge container registry.
+# Release workflow: on a final version tag, build the four release archives
+# and SHA256SUMS and verify them, then build one multi-arch server image and
+# push the same digest to the forge container registry first and then to
+# Docker Hub, compare the digests, push the Helm chart as an OCI artifact to
+# the forge container registry, and last create a draft release that holds
+# the archives and SHA256SUMS. A person with release permission reviews the
+# draft and publishes it. The workflow never publishes a release.
 #
 # The Docker Hub push needs two push-only repository secrets,
 # DOCKERHUB_USERNAME and DOCKERHUB_TOKEN. Before the first run, a person
@@ -14,8 +17,8 @@
 #   gh api repos/docker/setup-qemu-action/commits/v3.7.0 --jq .sha
 #
 # The digest is shown in the workflow summary. It is not added to release
-# notes: the job grants contents:read, and editing a release needs
-# contents:write. The owner confirms that trade-off in the pull request.
+# notes: the publish job grants contents:read, and only the release-draft
+# job has contents:write.
 
 name: release
 
@@ -24,20 +27,91 @@
     tags:
       - "v*"
 
-permissions:
-  contents: read
-  packages: write
+# No job inherits a permission. Each job names its own.
+permissions: {}
 
 env:
   FORGE_IMAGE: ghcr.io/opendustpan/opendustpan
   HUB_IMAGE: docker.io/opendustpan/opendustpan
 
 jobs:
+  archives-build:
+    # Same guard as the publish job: a tag event in the main repository.
+    if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'
+    runs-on: ubuntu-latest
+    permissions:
+      contents: read
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+        with:
+          ref: ${{ github.ref }}
+          persist-credentials: false
+
+      - name: Check that the tag is a final version tag
+        env:
+          TAG: ${{ github.ref_name }}
+        run: |
+          if ! echo "$TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
+            echo "::error::Not a final version tag (vX.Y.Z with no suffix). This run builds no archive."
+            exit 1
+          fi
+
+      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
+        with:
+          go-version-file: go.mod
+          cache: false
+
+      - name: Build the archives and SHA256SUMS
+        env:
+          RELEASE_VERSION: ${{ github.ref_name }}
+          RELEASE_OUT: ${{ runner.temp }}/release
+        run: ./scripts/release-build.sh "$RELEASE_VERSION" "$RELEASE_OUT"
+
+      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
+        with:
+          name: release-archives
+          path: ${{ runner.temp }}/release/
+          retention-days: 3
+          if-no-files-found: error
+
+  archives-verify:
+    # Reads the files that the build job stored, not the build directory.
+    if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'
+    needs: archives-build
+    runs-on: ubuntu-latest
+    permissions:
+      contents: read
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+        with:
+          ref: ${{ github.ref }}
+          persist-credentials: false
+
+      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
+        with:
+          name: release-archives
+          path: ${{ runner.temp }}/release
+
+      - name: Verify the archives and SHA256SUMS
+        env:
+          RELEASE_VERSION: ${{ github.ref_name }}
+          RELEASE_OUT: ${{ runner.temp }}/release
+        run: |
+          ./scripts/release-verify.sh "$RELEASE_VERSION" "$RELEASE_OUT"
+          # Defense in depth: release-verify.sh compares each checksum too;
+          # this check stays so that the file that the next job uploads is
+          # checked by a second tool.
+          (cd "$RELEASE_OUT" && sha256sum --check --strict SHA256SUMS)
+
   publish:
     # The publish job runs only on a tag event in the main repository.
     # Fork runs and pull_request runs stop here and read no token.
     if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'
+    needs: archives-verify
     runs-on: ubuntu-latest
+    permissions:
+      contents: read
+      packages: write
     steps:
       - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
         with:
@@ -233,3 +307,57 @@
           fi
           helm package deploy/helm/opendustpan --version "$VERSION" --app-version "$VERSION"
           helm push "opendustpan-$VERSION.tgz" oci://ghcr.io/opendustpan/charts
+
+  release-draft:
+    # The only job with contents:write. It runs after the archives pass the
+    # check and after the image and the chart are pushed, and it creates a
+    # draft: no one can download from a draft until a person publishes it.
+    if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'
+    needs: [archives-verify, publish]
+    runs-on: ubuntu-latest
+    permissions:
+      contents: write
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+        with:
+          ref: ${{ github.ref }}
+          persist-credentials: false
+
+      # Defense in depth: the publish job checks the actor and the tag too;
+      # these two steps stay so that this job never relies on that job.
+      - name: Check that the actor has release permission
+        env:
+          ACTOR: ${{ github.actor }}
+          REPO: ${{ github.repository }}
+          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
+        run: |
+          PERMISSION="$(gh api "repos/$REPO/collaborators/$ACTOR/permission" --jq .permission)" || {
+            echo "::error::Release permission check failed for the actor. An actor with release permission must start the tag."
+            exit 1
+          }
+          if [ "$PERMISSION" != "admin" ] && [ "$PERMISSION" != "maintain" ]; then
+            echo "::error::The actor has repository permission $PERMISSION, not release permission. An actor with release permission must start the tag."
+            exit 1
+          fi
+
+      - name: Check that the tag is a final version tag
+        env:
+          TAG: ${{ github.ref_name }}
+        run: |
+          if ! echo "$TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
+            echo "::error::Not a final version tag (vX.Y.Z with no suffix). This run creates no release."
+            exit 1
+          fi
+
+      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
+        with:
+          name: release-archives
+          path: ${{ runner.temp }}/release
+
+      - name: Create the draft release
+        env:
+          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
+          GH_REPO: ${{ github.repository }}
+          RELEASE_VERSION: ${{ github.ref_name }}
+          RELEASE_OUT: ${{ runner.temp }}/release
+        run: ./scripts/release-upload.sh "$RELEASE_VERSION" "$RELEASE_OUT"
```

Reading guide for the review:

- The three new jobs have the same `if:` line as `publish`. The existing test helper `jobGuardClauses` (`e2e/release_workflow_test.go:202`) reads the last job-level `if:`, so the lines must stay the same text.
- `publish` changes only in its header: `needs`, and a `permissions` block in place of the one for the file.
- Both jobs that download the artifact read the name `release-archives`. The artifact keeps for three days. It holds the files that the draft holds and no secret.

## Risks

- The first real run is the first tag. See "Test path with no tag" for what no test shows.
- A run that fails in `release-draft` after the image push leaves the image and the chart out with no draft. "Re-run failed jobs" runs only `release-draft`. "Re-run all jobs" stops in `publish`, and no draft follows (decision 12). The maintainer section says which button to use.
- `ubuntu-latest` is a moving label. The change compares no two runs, so a change of the runner image cannot make a check fail by itself.
- The Droid computer applies the workflow patch by hand. A mistake in that step shows in the workflow test of the second pull request.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| registry pushes before the archives pass the check | 0 | pushes | one tag run |
| jobs with `contents: write` | 1 | jobs | `release.yml` |
| new `run:` blocks with a template value | 0 | blocks | `release.yml` |
| `gh` calls after a failed check | 0 | calls | `release-upload.sh`, any hostile case |
| pins that are not a full SHA | 0 | pins | `release.yml` |
