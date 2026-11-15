## ADDED Requirements

### Requirement: collect-scope

When the docs check runs, the docs check SHALL list each fenced block tagged sh, bash or shell in docs/guides, docs/reference and README through CollectCommands.

#### Scenario: Guide block listed with index
- **WHEN** the docs check reads a fenced block tagged sh, bash or shell in docs/guides
- **THEN** CollectCommands lists the block with its page path and block index

#### Scenario: Reference and README blocks listed
- **WHEN** the docs check reads docs/reference and README
- **THEN** CollectCommands lists each block with its page path and block index

### Requirement: corpus-shape

When the docs check reads the corpus shape, the docs check SHALL enforce backtick fences, tilde fences, first-word info match, unclosed-fence failure, missing-path failure, empty-corpus failure, and newDocsCommand rejection of empty text and text over 65536 bytes.

#### Scenario: Tilde fence and extra info words listed
- **WHEN** a block uses a tilde fence or an info string such as bash with extra words
- **THEN** CollectCommands lists the block with its page path and block index

#### Scenario: Missing directory fails
- **WHEN** docs/guides, docs/reference or README is missing
- **THEN** the docs check fails the gate with the missing path

#### Scenario: Empty corpus fails
- **WHEN** the scope holds zero in-scope blocks
- **THEN** the docs check fails the gate with zero blocks found

#### Scenario: Unclosed fence fails
- **WHEN** a page holds an unclosed fence
- **THEN** the docs check fails the gate with the page path

#### Scenario: Empty and oversize text rejected
- **WHEN** CollectCommands reads empty text or text over 65536 bytes
- **THEN** newDocsCommand rejects the block

### Requirement: run-exact-text

When the docs check runs a listed block that carries no skip marker, the docs check SHALL run the exact block bytes through RunCommands with no rewrite.

#### Scenario: Guide command runs as written
- **WHEN** the docs check runs a listed block that carries no skip marker
- **THEN** RunCommands runs the exact block bytes in a per-page work directory in page order and the command exits zero within 300 seconds

### Requirement: page-setup

When a block needs a running server, prior state or an access token, the docs check SHALL run setup from earlier blocks of the same page or require needs-secret or needs-human on that block.

#### Scenario: Server state comes from same page
- **WHEN** a block needs a running server, prior state or an access token
- **THEN** RunCommands runs the earlier setup blocks first in the same per-page work directory

#### Scenario: External secret needs marker
- **WHEN** no earlier block of the same page sets the needed server, state or token
- **THEN** the docs check requires needs-secret or needs-human on that block

### Requirement: unrun-block-fails

Where a fenced block sits in scope, the docs check SHALL fail the gate when no RunCommands result covers that block.

#### Scenario: Untested block fails the gate
- **WHEN** a fenced block in scope has no RunCommands result
- **THEN** the docs check fails the gate with the page path and the block index

### Requirement: exception-mark-skips

Where a block carries docs-check-skip with a reason from the closed allowlist, the docs check SHALL skip the run through RunCommands.

#### Scenario: Marked block skips with reason
- **WHEN** a block carries docs-check-skip with a reason from the closed allowlist on the line before the fence
- **THEN** RunCommands marks the block skipped with the page path, block index and marker reason

### Requirement: stale-entry-fails

Where a skip marker names a reason outside the closed allowlist or a block that no longer exists, the docs check SHALL fail the gate.

#### Scenario: Unknown reason fails
- **WHEN** a skip marker names a reason outside needs-secret, needs-human, needs-docker and destructive-outside-fixture
- **THEN** the docs check fails the gate with the page path and block index

#### Scenario: Stale marker fails
- **WHEN** a skip marker names a block that no longer exists at that page path and block index
- **THEN** the docs check fails the gate with the page path and block index

### Requirement: failure-report

When a block exits nonzero or exceeds its timeout, the docs check SHALL report the page path, block index, command text and captured output.

#### Scenario: Nonzero exit reports output
- **WHEN** a block exits nonzero
- **THEN** the docs check reports The docs check failed on page <page> block <index> with command text and captured output

#### Scenario: Timeout kills and reports
- **WHEN** a block exceeds 300 seconds per block
- **THEN** the docs check kills the whole process group including child processes and reports The docs check failed on page <page> block <index> with command text and captured output

### Requirement: make-target

When the operator runs the check, the docs check SHALL provide the NEW make target docs-commands-check that runs CollectCommands and RunCommands and prints the failure report.

#### Scenario: Help command shows failure
- **WHEN** the operator runs make docs-commands-check after a failure
- **THEN** the target prints page path, block index, command text and captured output for each failed block

### Requirement: docker-gate

When a block needs Docker and E2E_COMPOSE is unset, the docs check SHALL mark the block skipped with reason needs-docker through RunCommands.

#### Scenario: Docker block skips without flag
- **WHEN** a block needs Docker and E2E_COMPOSE is unset
- **THEN** RunCommands marks the block skipped with reason needs-docker

#### Scenario: Docker use without marker fails
- **WHEN** block text invokes docker without a needs-docker marker
- **THEN** the docs check fails the gate with the page path and block index

### Requirement: sandbox-contain

When RunCommands runs a block, the docs check SHALL confine the block to a fresh per-page work directory with the closed environment allowlist and refuse sudo and writes outside that directory.

#### Scenario: Destructive command contained
- **WHEN** a block invokes sudo or writes outside its per-page work directory
- **THEN** RunCommands refuses the run without host change

#### Scenario: Child processes die on timeout
- **WHEN** a block exceeds 300 seconds per block
- **THEN** no child process or background server from the block stays running

#### Scenario: Output capped and redacted
- **WHEN** a block prints output that exceeds the cap or holds a token
- **THEN** captured output keeps at most 1048576 bytes with token strings redacted
