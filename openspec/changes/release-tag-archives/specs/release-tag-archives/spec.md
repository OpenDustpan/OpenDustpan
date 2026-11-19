## ADDED Requirements

### Requirement: The tag workflow builds and checks the archives before any push

When a final version tag is pushed in the main repository, the release workflow SHALL build the four release archives and `SHA256SUMS` for the tag, SHALL check them in a second job from the stored files, and SHALL start no registry push until that check passes.

#### Scenario: Archives pass before the image is pushed
- **WHEN** the tag `v0.1.0` is pushed and the archives pass the check
- **THEN** the image job starts after the check job, and the draft job starts after the image job.

#### Scenario: A bad archive stops the run
- **WHEN** an archive or `SHA256SUMS` fails the check
- **THEN** the verify job fails, and no image, no chart and no release is pushed.

#### Scenario: A tag that is not a final version
- **WHEN** a tag such as `v0.1.0-rc1` or `v1` is pushed
- **THEN** no job builds an archive, and the run says that the tag is not a final version tag.

#### Scenario: A tag with no release record
- **WHEN** the release record has no line for the tag
- **THEN** the build job fails before any archive exists, and the message names `make release-record-check`.

### Requirement: The release is a draft that a person publishes

When the archives and the registry pushes pass, the release workflow SHALL create one draft release for the tag that holds the four archives and `SHA256SUMS`. It SHALL NOT publish the release, and it SHALL NOT change a release that exists.

#### Scenario: Draft with five files
- **WHEN** the draft job runs for `v0.1.0` after a passed check
- **THEN** one draft release `v0.1.0` exists with exactly `SHA256SUMS` and the four files `opendustpan_v0.1.0_<os>_<arch>.tar.gz` for linux and darwin on amd64 and arm64, and no publish call was made.

#### Scenario: A release for the tag exists
- **WHEN** a release, draft or published, has the tag
- **THEN** the upload step refuses and changes nothing, and the message names `gh release list`.

#### Scenario: The upload fails after the draft exists
- **WHEN** `gh` fails after it created the draft
- **THEN** the step fails, the draft stays a draft, and the message names the delete command and says to re-run the failed job.

#### Scenario: No tag, no release
- **WHEN** a pull request, a branch push or a fork event starts the repository workflows
- **THEN** no job of the release workflow runs, and no release is made.

### Requirement: The upload checks the set itself

When `scripts/release-upload.sh` runs, it SHALL check the version, run `scripts/release-verify.sh`, check the shape of the set against an allowlist (exactly the five names as regular files, exactly one SHA256SUMS line for each archive and no other line), and run `sha256sum --check --strict SHA256SUMS` on the files in its directory, and call `gh` only after all pass.

#### Scenario: A changed archive
- **WHEN** one byte of an archive differs from its `SHA256SUMS` line
- **THEN** the script exits with a non-zero status and makes no `gh` call, also when `release-verify.sh` is replaced by a script that passes.

#### Scenario: A hostile set
- **WHEN** `SHA256SUMS` names another file, lacks an archive, repeats a line, or holds any other line, or the directory holds an extra file or a link
- **THEN** the script exits with a non-zero status and makes no `gh` call, also when `release-verify.sh` is replaced by a script that passes.

#### Scenario: A bad version
- **WHEN** the version holds `$(id)`, a backtick, a semicolon, a space, a newline, a leading dash, a suffix, or more than 64 characters
- **THEN** the script exits with a non-zero status, names the allowed form and makes no `gh` call.

#### Scenario: No secret in the output
- **WHEN** any step of the script fails while `GH_TOKEN` holds a value
- **THEN** the value is in no output of the script and in no argument of a `gh` call.

### Requirement: Each job has the least permission

The release workflow SHALL grant no permission for the whole file, SHALL grant `contents: write` to the draft job only, and SHALL grant `packages: write` to the image job only.

#### Scenario: Permissions per job
- **WHEN** the workflow file is read
- **THEN** the file default is empty, the archive jobs have `contents: read`, the image job has `contents: read` and `packages: write`, and only the draft job has `contents: write`.

#### Scenario: No credential after checkout
- **WHEN** a job of the workflow checks out the repository for the archives
- **THEN** the checkout step has `persist-credentials: false`.

### Requirement: Actions are pinned and the tag is data

Each action of the release workflow SHALL be pinned by its full commit SHA with the release in a comment, and no `run:` block SHALL hold a template expression.

#### Scenario: Pinned actions
- **WHEN** the workflow file is read
- **THEN** each `uses:` value ends in a 40-digit hexadecimal SHA followed by a comment that names the release.

#### Scenario: The tag name reaches a shell as data
- **WHEN** the workflow file is read
- **THEN** no `run:` block holds `${{`, and each step that uses the tag reads it from `env:`.

### Requirement: The path is tested with no tag

The tests of the archive upload and of the workflow structure SHALL run in the Go job of each pull request and need no tag, no token and no registry.

#### Scenario: Pull request run
- **WHEN** a pull request runs the Go job
- **THEN** `TestReleaseUploadCreatesOneDraft` builds the archives of a test version in a copy of the work tree, runs the upload script with a fake `gh`, and passes.

#### Scenario: Weak workflow
- **WHEN** the checker reads a workflow that lacks a guard, a `needs`, a permission block, a pin or the environment form of the tag
- **THEN** the checker reports a finding for each missing item.
