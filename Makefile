# Local gates. CI runs the same checks (.github/workflows/ci.yml).
# Tool versions are pinned here and in CI; install the matching binaries:
#   golangci-lint v2.14.0  https://golangci-lint.run/welcome/install/
#   sqlc          v1.31.1  https://docs.sqlc.dev/en/latest/overview/install.html
#   semgrep       1.178.0  https://semgrep.dev/docs/getting-started/quickstart
#   trivy         v0.75.0  https://trivy.dev/latest/getting-started/installation/
#   trufflehog    v3.99.2  https://github.com/trufflesecurity/trufflehog#floppy_disk-installation
#   python3       any 3.x, standard library only (for docs-lint)
# nilaway and govulncheck need no install: go run builds the pinned versions below.

.PHONY: check fmt lint nilaway vuln semgrep trivy trufflehog licenses docs-lint test test-postgres test-s3 bench load e2e upgrade-test crash-test soak generate build image-smoke

check: lint nilaway vuln semgrep trivy trufflehog licenses docs-lint test bench journal-guard ## Everything CI checks.

fmt: ## Format Go code (gofumpt, gci, golines).
	golangci-lint fmt ./...

lint: ## Strict lint, tidy and generated-code checks for Go.
	go mod tidy -diff
	sqlc diff
	go test ./internal/pulumijournal/internal/gen/ -run TestGenerateMatchesCommittedFile -count=1
	golangci-lint run ./...

# The same build tags as .golangci.yml, so the e2e and upgrade packages are checked too.
ANALYSIS_TAGS := e2e,upgrade,soak
NILAWAY       := go run go.uber.org/nilaway/cmd/nilaway@v0.0.0-20260918162853-acb8859b9031
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@v1.8.0

nilaway: ## Nil-panic analysis of our packages; dependencies are read but not reported.
	GOFLAGS=-tags=$(ANALYSIS_TAGS) $(NILAWAY) -include-pkgs=opendustpan.org/opendustpan ./...

vuln: ## Known vulnerabilities in the code paths we call (reads the database at vuln.go.dev).
	$(GOVULNCHECK) -tags $(ANALYSIS_TAGS) ./...

# The rule packs come from the Semgrep registry on each run. No metrics are sent.
# use-of-md5 is excluded: gosec reports the same lines, and each one has a
# nolint with its reason.
semgrep: ## Semgrep registry rules for Go, the Dockerfile, the workflows and secrets.
	semgrep scan --error --metrics=off --disable-version-check --quiet \
		--config p/golang --config p/dockerfile --config p/github-actions --config p/secrets \
		--exclude-rule go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5 .

# The vulnerability database and the misconfiguration checks come from
# mirror.gcr.io on each run. No telemetry is sent. Each accepted finding is a
# line in .trivyignore with its reason.
#
# The license scan is a second run, because --severity applies to each scanner
# of a run. Trivy gives each license a class and a severity. The scan fails on
# a forbidden license (CRITICAL, for example AGPL), a restricted license
# (HIGH, for example GPL) and an unknown license. It passes a notice license
# (LOW: MIT, BSD, Apache-2.0) and a reciprocal license (MEDIUM: MPL-2.0),
# which docs/research/licenses.md accepts. Trivy reads the license of a Go
# module from the module cache, so go mod download runs first: with an empty
# cache the scan finds no license and passes.
trivy: ## Trivy: known vulnerabilities in dependencies, misconfigurations (Dockerfile), secrets in the tree, and dependency licenses.
	trivy fs --scanners vuln,misconfig,secret --exit-code 1 \
		--disable-telemetry --skip-version-check --no-progress .
	go mod download
	trivy fs --scanners license --severity UNKNOWN,HIGH,CRITICAL --exit-code 1 \
		--disable-telemetry --skip-version-check --no-progress .

# Scans the commits of every local branch, not the uncommitted files. Each
# candidate is sent to its provider for verification. "unknown" is a candidate
# that could not be verified (for example, the provider did not answer), so a
# network failure can fail this target. Exit code 183 means a secret was found.
trufflehog: ## TruffleHog: verified secrets in the Git history.
	trufflehog git file://. --results=verified,unknown --fail --no-update

# The same platform pairs as scripts/release-build.sh. The release archives
# and the container image ship these files (third_party_licenses/).
RELEASE_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

licenses: ## Each module linked into the binary has a license file to ship, on each release platform.
	./scripts/third-party-licenses.sh --check $(RELEASE_PLATFORMS)

# Prose that ADR 14 holds to ASD-STE100. Accepted ADRs before ADR 14 are exempt:
# ADR 1 forbids editing them.
STE_LINT  := python3 -I .agents/skills/asd-ste100/scripts/ste-lint.py
DOCS      := $(wildcard README.md CLAUDE.md GLOSSARY.md CONTRIBUTING.md SECURITY.md SUPPORT.md docs/guides/*.md docs/reference/*.md docs/explanation/*.md)
DOCS_ADRS := $(shell ls docs/adr/*.md | awk -F/ 'substr($$3, 1, 4) + 0 >= 14')

# Words that claim quality instead of showing it, and emoji. The list and its
# self-test are in the script.
BANNED    := python3 -I scripts/docs-banned-words.py

docs-lint: ## ASD-STE100 structural rules and banned words for README, CLAUDE.md, GLOSSARY.md, the community files, the guides, the reference, the explanations and ADRs from 14 on.
	$(STE_LINT) $(DOCS) $(DOCS_ADRS)
	$(BANNED) --selftest
	$(BANNED) $(DOCS) $(DOCS_ADRS)

test: ## Go tests; the PostgreSQL suite is skipped unless DUSTPAN_TEST_POSTGRES_URL is set.
	go test -race -count=1 ./...

test-postgres: ## Go tests including PostgreSQL (needs DUSTPAN_TEST_POSTGRES_URL).
	@test -n "$$DUSTPAN_TEST_POSTGRES_URL" || (echo "set DUSTPAN_TEST_POSTGRES_URL" && exit 1)
	go test -race -count=1 ./pkg/store/postgres/...

bench: ## Fast benchmarks (delta apply, gzip, checkpoint store) with one iteration each; proves they compile and run.
	go test -run '^$$' -bench . -benchtime 1x ./pkg/stack/ ./pkg/store/sqlite/

load: ## Run the load harness against a self-started server and write load-report.json (SQLite default; set DUSTPAN_TEST_POSTGRES_URL for PostgreSQL). Not part of check.
	go run ./load -out load-report.json

test-s3: ## Go tests in pkg/blob/s3 against a live S3-compatible server (needs DUSTPAN_TEST_S3_*).
	@test -n "$$DUSTPAN_TEST_S3_ENDPOINT" && test -n "$$DUSTPAN_TEST_S3_BUCKET" && test -n "$$DUSTPAN_TEST_S3_ACCESS_KEY" && test -n "$$DUSTPAN_TEST_S3_SECRET_KEY" || (echo "set DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, DUSTPAN_TEST_S3_ACCESS_KEY and DUSTPAN_TEST_S3_SECRET_KEY" && exit 1)
	go test -race -count=1 ./pkg/blob/s3/...

e2e: ## Drive real Pulumi CLI and OpenTofu versions against the server (downloads CLIs; see e2e/doc.go). Not part of check; CI runs it as its own job.
	go test -tags e2e -race -count=1 -parallel 16 -timeout 60m ./e2e/...

e2e-journal: ## Drive the real Pulumi CLI through the journal route on the recording build (ADR 43, work item 9). Not part of check.
	go test -tags "e2e journalrecording" -race -count=1 -run TestPulumiJournalingOn -timeout 60m ./e2e/...

upgrade-test: ## Restart the current binary on the same database and check data, secrets and the held lock survive (downloads CLIs; empty-database case without a v* tag, previous-tag case with one). Not part of check.
	E2E_SERVER=binary go test -tags "e2e upgrade" -race -count=1 -v -timeout 20m ./e2e/ -run TestUpgrade

crash-test: ## Stop the server binary with SIGKILL during a Pulumi checkpoint save, an OpenTofu upload and a lock, then check recovery on the same database and blobs (downloads CLIs; PostgreSQL with DUSTPAN_TEST_POSTGRES_URL). Not part of check.
	E2E_SERVER=binary go test -tags e2e -race -count=1 -v -timeout 20m ./e2e/ -run TestBinaryCrash

SOAK_DURATION ?= 30m
SOAK_PULUMI ?= 4
SOAK_TOFU ?= 4
SOAK_REPORT_DIR ?= soak-reports

soak: ## Run concurrent real Pulumi CLI and OpenTofu processes against one server binary for SOAK_DURATION (default 30m; SOAK_DURATION=60s for a smoke check), with SOAK_PULUMI and SOAK_TOFU workers each (default 4) writing CSV and Markdown reports to SOAK_REPORT_DIR (SQLite default; PostgreSQL with DUSTPAN_TEST_POSTGRES_URL). Not part of check; see docs/guides/soak.md.
	SOAK_DURATION=$(SOAK_DURATION) SOAK_PULUMI=$(SOAK_PULUMI) SOAK_TOFU=$(SOAK_TOFU) SOAK_REPORT_DIR=$(SOAK_REPORT_DIR) E2E_SERVER=binary go test -tags "e2e soak" -race -count=1 -v -timeout 60m ./e2e/ -run TestSoak

generate: ## Regenerate sqlc query code after editing queries.sql or migrations, and the settings reference after editing the CLI declarations.
	sqlc generate
	go test ./internal/cli/ -run TestConfigurationReference -update -count=1
	go test ./internal/pulumijournal/internal/gen/ -run TestGenerateMatchesCommittedFile -update -count=1

journal-guard: ## Refuse a release build that uses the journalrecording tag (ADR 43, work item 9).
	@if grep -rq journalrecording Dockerfile .github/workflows scripts 2>/dev/null || \
		sed -n '/^build:/,/^[a-z-]*:/p' Makefile | grep -q journalrecording; then \
		echo "journal-guard: a release path uses the journalrecording tag. Remove it."; exit 1; \
	fi

build: ## Build the server binary.
	go build -ldflags "-X main.version=$$(git describe --tags --always --dirty)" -o bin/opendustpan ./cmd/opendustpan

image-smoke: ## Build the container image and run the local smoke test (needs docker; not part of check).
	./scripts/smoke-image.sh
