# Local gates. CI runs the same checks (.github/workflows/ci.yml).
# Tool versions are pinned here and in CI; install the matching binaries:
#   golangci-lint v2.14.0  https://golangci-lint.run/welcome/install/
#   sqlc          v1.31.1  https://docs.sqlc.dev/en/latest/overview/install.html
#   semgrep       1.178.0  https://semgrep.dev/docs/getting-started/quickstart
#   trivy         v0.75.0  https://trivy.dev/latest/getting-started/installation/
#   trufflehog    v3.99.2  https://github.com/trufflesecurity/trufflehog#floppy_disk-installation
#   python3       any 3.x, standard library only (for docs-lint)
# nilaway and govulncheck need no install: go run builds the pinned versions below.

.PHONY: regenerate-baseline check fmt lint nilaway vuln semgrep trivy trivy-canary trufflehog licenses docs-lint chart-lint test test-nocache test-postgres test-s3 bench load e2e upgrade-test crash-test soak faults generate build image-smoke compose-smoke demo checks-agent clients-conformance

check: lint lint-journal-build nilaway vuln semgrep trivy trufflehog licenses docs-lint test bench journal-guard journal-test ## Everything CI checks.

fmt: ## Format Go code (gofumpt, gci, golines).
	golangci-lint fmt ./...

lint: ## Strict lint, tidy and generated-code checks for Go.
	go mod tidy -diff
	sqlc diff
	go test ./internal/pulumijournal/internal/gen/ -run TestGenerateMatchesCommittedFile -count=1
	python3 -I scripts/runner-deps-guard.py --selftest
	python3 -I scripts/runner-deps-guard.py
	python3 -I scripts/module-age-gate.py --selftest
	python3 -I scripts/module-age-gate.py
	golangci-lint run ./...

lint-journal-build: ## Lint the journal recording build: some files are left out of it, so it needs its own run.
	golangci-lint run --build-tags e2e,journalrecording ./...

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
# mirror.gcr.io on each run. No telemetry is sent. Each accepted finding is an
# entry in .trivyignore.yaml, scoped by path, with its reason in statement.
#
# The license scan writes a JSON report, and scripts/trivy-license-gate.py checks
# it. The gate fails on an unknown, restricted (HIGH, for example GPL) or
# forbidden (CRITICAL, for example AGPL) license. It passes a notice license
# (LOW: MIT, BSD, Apache-2.0) and a reciprocal license (MEDIUM: MPL-2.0), which
# docs/research/licenses.md accepts. The scan does not filter by --severity: that
# filter also drops the LOW results, so the scan found nothing and still passed.
# Trivy reads the license of a Go module from the module cache, so go mod download
# runs first. The gate also fails when a go.mod requirement has no license result,
# so an empty cache cannot pass. The canary fixture under testdata/trivy-canary
# requires a GPL module. The gate must flag it, or the scan is not trusted. The
# real scan skips that fixture, so it does not fail on the fixture.
TRIVY_CANARY_MODULE := github.com/xen0n/gosmopolitan@v1.3.0
TRIVY_LICENSE_FLAGS := --scanners license --disable-telemetry --skip-version-check --no-progress

trivy: ## Trivy: known vulnerabilities in dependencies, misconfigurations (Dockerfile, Helm chart output), secrets in the tree, and dependency licenses.
	trivy fs --scanners vuln,misconfig,secret --exit-code 1 --ignorefile .trivyignore.yaml \
		--disable-telemetry --skip-version-check --no-progress .
	go mod download
	go mod download $(TRIVY_CANARY_MODULE)
	mkdir -p bin
	trivy fs $(TRIVY_LICENSE_FLAGS) --skip-dirs testdata/trivy-canary \
		--format json --output bin/trivy-license.json .
	python3 -I scripts/trivy-license-gate.py gate bin/trivy-license.json go.mod
	$(MAKE) trivy-canary

trivy-canary: ## Trivy license canary: the gate must flag the GPL fixture in testdata/trivy-canary, or the scan is not trusted.
	go mod download $(TRIVY_CANARY_MODULE)
	mkdir -p bin
	trivy fs $(TRIVY_LICENSE_FLAGS) --format json --output bin/trivy-canary.json testdata/trivy-canary
	python3 -I scripts/trivy-license-gate.py canary bin/trivy-canary.json

# Scans the commits of every local branch, not the uncommitted files. Each
# candidate is sent to its provider for verification. "unknown" is a candidate
# that could not be verified (for example, the provider did not answer), so a
# network failure can fail this target. Exit code 183 means a secret was found.
# .trufflehog-exclude-paths.txt lists files that are known false positives,
# one regular expression per line. Each entry needs the owner's approval.
# Today it holds one vendored agent skill, where a Markdown link that names a
# file looks like a credential to a detector, and six test files whose fake
# URLs with a user name and a password test the masking of credentials.
trufflehog: ## TruffleHog: verified secrets in the Git history.
	trufflehog git file://. --results=verified,unknown --fail --no-update --exclude-paths=.trufflehog-exclude-paths.txt

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

chart-lint: ## Lint and render the Helm chart (helm lint, helm template, kubeconform). Needs helm and kubeconform; not part of check.
	./scripts/chart-lint.sh

# The store packages run for more than 10 minutes, the default limit of go test,
# on a CI runner with 4 cores. GO_TEST_TIMEOUT is the limit for each package.
GO_TEST_TIMEOUT := 20m

# With -race the compiler also adds checkptr checks to every package. In the
# SQLite engine, which is C translated to Go with much unsafe pointer
# arithmetic, these checks took about 30% of the CPU of the SQLite store
# tests. RACE_TEST_FLAGS turns checkptr off for modernc.org/... only: the race
# detector stays on for all code, and checkptr stays on for our own code. On
# 4 cores, the dev data suite of pkg/store/sqlite used 904 s of user CPU with
# the checks and 647 s without them. The cost: checkptr does not find unsafe
# pointer misuse inside those modules, which we do not maintain.
RACE_TEST_FLAGS := -race -gcflags='modernc.org/...=-d=checkptr=0'

test: ## Go tests; the PostgreSQL suite is skipped unless DUSTPAN_TEST_POSTGRES_URL is set. A package whose code, files and environment did not change gives its cached result.
	go test $(RACE_TEST_FLAGS) -timeout $(GO_TEST_TIMEOUT) ./...

test-nocache: ## Go tests as in test, but every package runs again. CI runs this on main every 2 hours to find flaky tests.
	go test $(RACE_TEST_FLAGS) -count=1 -timeout $(GO_TEST_TIMEOUT) ./...

test-postgres: ## Go tests including PostgreSQL (needs DUSTPAN_TEST_POSTGRES_URL).
	@test -n "$$DUSTPAN_TEST_POSTGRES_URL" || (echo "set DUSTPAN_TEST_POSTGRES_URL" && exit 1)
	go test $(RACE_TEST_FLAGS) -count=1 -timeout $(GO_TEST_TIMEOUT) ./pkg/store/postgres/...

bench: ## Fast benchmarks (delta apply, gzip, checkpoint store) with one iteration each; proves they compile and run.
	go test -run '^$$' -bench . -benchtime 1x ./pkg/stack/ ./pkg/store/sqlite/

load: ## Run the load harness against a self-started server and write load-report.json (SQLite default; set DUSTPAN_TEST_POSTGRES_URL for PostgreSQL). Not part of check.
	go run ./load -out load-report.json

test-s3: ## Go tests in pkg/blob/s3 against a live S3-compatible server (needs DUSTPAN_TEST_S3_*).
	@test -n "$$DUSTPAN_TEST_S3_ENDPOINT" && test -n "$$DUSTPAN_TEST_S3_BUCKET" && test -n "$$DUSTPAN_TEST_S3_ACCESS_KEY" && test -n "$$DUSTPAN_TEST_S3_SECRET_KEY" || (echo "set DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, DUSTPAN_TEST_S3_ACCESS_KEY and DUSTPAN_TEST_S3_SECRET_KEY" && exit 1)
	go test -race -count=1 ./pkg/blob/s3/...

e2e: ## Drive real Pulumi CLI and OpenTofu versions against the server (downloads CLIs; see e2e/doc.go). Not part of check; CI runs it as its own job.
	go test -tags e2e -race -count=1 -parallel 16 -timeout 60m ./e2e/...

clients-conformance: ## Build the server, generate the Go client from the description that the server serves, and run the client conformance suite on it. Not part of check.
	DUSTPAN_TEST_CLIENTGEN_CONFORMANCE=1 go test -race -count=1 -timeout 15m -run TestGoClientConformance ./internal/clientgen/conformance/

checks-agent: ## Sync the branch with main, then run generate, check, e2e and a TruffleHog scan of the branch commits with cleanup (SYNC=merge|rebase|none, SKIP="steps", CLEAN_CACHE=1 also deletes the e2e CLI cache).
	./scripts/checks-agent.sh

# With TOXIPROXY_URL set, the target uses that Toxiproxy server (CI runs it
# as a service container) and downloads no binary.
faults: ## Run Toxiproxy network fault tests against a self-started server (downloads toxiproxy-server unless TOXIPROXY_URL is set; test-only, never ships). Not part of check.
	if [ -z "$$TOXIPROXY_URL" ]; then TOXIPROXY_BIN="$$(./scripts/toxiproxy.sh)" && export TOXIPROXY_BIN; fi && \
	go test -tags faults -race -count=1 -timeout 20m ./e2e/faults/...

e2e-journal: ## Drive the real Pulumi CLI through the journal route on the recording build (ADR 43, work item 9). Not part of check.
	E2E_PULUMI_VERSIONS=3.268.0 go test -tags "e2e journalrecording" -race -count=1 -run 'TestPulumiJournalingOn|TestPulumiJournalRefusalCancel|TestPulumiJournalingOnVersion|TestPulumiJournalStateMigration' -timeout 60m ./e2e/...

upgrade-test: ## Restart the current binary on the same database and check data, secrets and the held lock survive (downloads CLIs; empty-database case without a v* tag, previous-tag case with one). Not part of check.
	E2E_SERVER=binary go test -tags "e2e upgrade" -race -count=1 -v -timeout 20m ./e2e/ -run TestUpgrade

crash-test: ## Stop the server binary with SIGKILL during a Pulumi checkpoint save, an OpenTofu upload and a lock, then check recovery on the same database and blobs (downloads CLIs; PostgreSQL with DUSTPAN_TEST_POSTGRES_URL). Not part of check.
	E2E_SERVER=binary go test -tags e2e -race -count=1 -v -timeout 20m ./e2e/ -run TestBinaryCrash

SOAK_DURATION ?= 30m
SOAK_PULUMI ?= 4
SOAK_TOFU ?= 4
SOAK_REPORT_DIR ?= soak-reports

CONSOLE_REPO ?= https://github.com/OpenDustpan/console
CONSOLE_REF ?= b86703e174223d12fb211ad65b5f5ed0d71b896d
DEMO_PORT ?= 8080

soak: ## Run concurrent real Pulumi CLI and OpenTofu processes against one server binary for SOAK_DURATION (default 30m; SOAK_DURATION=60s for a smoke check), with SOAK_PULUMI and SOAK_TOFU workers each (default 4) writing CSV and Markdown reports to SOAK_REPORT_DIR (SQLite default; PostgreSQL with DUSTPAN_TEST_POSTGRES_URL). Not part of check; see docs/guides/soak.md.
	SOAK_DURATION=$(SOAK_DURATION) SOAK_PULUMI=$(SOAK_PULUMI) SOAK_TOFU=$(SOAK_TOFU) SOAK_REPORT_DIR=$(SOAK_REPORT_DIR) E2E_SERVER=binary go test -tags "e2e soak" -race -count=1 -v -timeout 60m ./e2e/ -run TestSoak

BASELINE_REF ?= origin/main

regenerate-baseline: ## Write the baseline migration of each dialect from the migrations of BASELINE_REF (default origin/main); needs DUSTPAN_TEST_POSTGRES_URL and pg_dump. Not part of check.
	./scripts/regenerate-baseline.sh $(BASELINE_REF)

generate: ## Regenerate sqlc query code after editing queries.sql or migrations, and the settings reference after editing the CLI declarations.
	sqlc generate
	go test ./internal/cli/ -run TestConfigurationReference -update -count=1
	go test ./internal/pulumijournal/internal/gen/ -run TestGenerateMatchesCommittedFile -update -count=1

journal-test: ## Journal route tests of the recording build. No CLI download; part of check.
	go test -tags journalrecording -race -count=1 ./internal/api/pulumi/ ./pkg/dustpan/

journal-guard: ## Refuse a release build that uses the journalrecording tag (ADR 43, work item 9).
	@if grep -rq journalrecording Dockerfile .github/workflows scripts 2>/dev/null || \
		sed -n '/^build:/,/^[a-z-]*:/p' Makefile | grep -q journalrecording; then \
		echo "journal-guard: a release path uses the journalrecording tag. Remove it."; exit 1; \
	fi

build: ## Build the server binary.
	go build -ldflags "-X main.version=$$(git describe --tags --always --dirty)" -o bin/opendustpan ./cmd/opendustpan

demo: build ## Run the server with the web console and demo data on 127.0.0.1:8080 (needs Go, Node and npm; not part of check).
	CONSOLE_REPO="$(CONSOLE_REPO)" CONSOLE_REF="$(CONSOLE_REF)" DEMO_PORT="$(DEMO_PORT)" ./scripts/demo.sh

image-smoke: ## Build the container image and run the local smoke test (needs docker; not part of check).
	./scripts/smoke-image.sh

compose-smoke: ## Start the stack of deploy/compose and drive the real Pulumi CLI and OpenTofu against it (needs docker with compose and openssl; not part of check).
	E2E_COMPOSE=1 go test -tags e2e -race -count=1 -timeout 30m -run '^TestComposeQuickstart$$' ./e2e/...
