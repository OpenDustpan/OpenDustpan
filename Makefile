# Local gates. CI runs the same checks (.github/workflows/ci.yml).
# Tool versions are pinned here and in CI; install the matching binaries:
#   golangci-lint v2.15.0  https://golangci-lint.run/welcome/install/
#   sqlc          v1.31.1  https://docs.sqlc.dev/en/latest/overview/install.html
#   semgrep       1.178.0  https://semgrep.dev/docs/getting-started/quickstart
#   trivy         v0.75.0  https://trivy.dev/latest/getting-started/installation/
#   trufflehog    v3.99.2  https://github.com/trufflesecurity/trufflehog#floppy_disk-installation
#   python3       any 3.x, standard library only (for docs-lint)
#   node 22 and tsc 6.0.2 (typescript on npm), only for clients-ts-test and clients-conformance
# nilaway and govulncheck need no install: go run builds the pinned versions below.

.PHONY: cover regenerate-baseline release-record-check check fmt lint subtestcheck nolint-check nilaway vuln semgrep trivy trivy-canary trufflehog licenses docs-lint adr-index adr-decisions chart-lint test test-nocache test-postgres test-s3 bench bench-s3 bench-s3-protocol load e2e e2e-ha upgrade-test mixed-version-test crash-test soak faults generate build build-launcher image-smoke compose-smoke demo checks-agent clients-conformance clients-generate clients-check clients-ts-test

check: lint subtestcheck lint-journal-build nilaway vuln semgrep trivy trufflehog licenses docs-lint release-record-check test bench journal-guard journal-test ## Everything CI checks.

fmt: ## Format Go code (gofumpt, gci, golines).
	golangci-lint fmt ./...

lint: ## Strict lint, tidy and generated-code checks for Go.
	go mod tidy -diff
	sqlc diff
	$(MAKE) --no-print-directory clients-check
	go test ./internal/pulumijournal/internal/gen/ -run TestGenerateMatchesCommittedFile -count=1
	python3 -I scripts/runner-deps-guard.py --selftest
	python3 -I scripts/runner-deps-guard.py
	python3 -I scripts/module-age-gate.py --selftest
	python3 -I scripts/module-age-gate.py
	go test -count=1 ./internal/adrnumbers
	python3 -I scripts/cover-summary.py --selftest
	golangci-lint run ./...

subtestcheck: ## Report a t.Error or t.Fatal that follows a parallel t.Run and reads a variable that the subtest writes.
	go run ./cmd/subtestcheck .

# The ref that nolint-check compares the branch with.
BASE ?= origin/main

nolint-check: ## Fail when the lines added since BASE (default origin/main) carry a suppression marker. Not part of check: CI skips it for the label nolint-approved.
	./scripts/check-nolint.sh '$(BASE)'

# Extra flags for golangci-lint, for example LINT_FLAGS=--timeout=15m in CI.
LINT_FLAGS ?=

lint-journal-build: ## Lint the journal recording build: some files are left out of it, so it needs its own run.
	golangci-lint run $(LINT_FLAGS) --build-tags e2e,journalrecording ./...

# The same build tags as .golangci.yml, so the e2e and upgrade packages are checked too.
ANALYSIS_TAGS := e2e,upgrade,soak
NILAWAY       := go run go.uber.org/nilaway/cmd/nilaway@v0.0.0-20260918162853-acb8859b9031
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@v1.8.0

# nilaway holds the whole program in memory. With the default GOGC it grew to
# 13.9 GB and the kernel killed it in a 15 GB container. A lower GOGC and a
# soft memory limit keep it inside such a container, at some extra CPU time.
# Set NILAWAY_GOGC or NILAWAY_GOMEMLIMIT to change them.
NILAWAY_GOGC      ?= 25
NILAWAY_GOMEMLIMIT ?= 12GiB

nilaway: ## Nil-panic analysis of our packages; dependencies are read but not reported.
	GOGC=$(NILAWAY_GOGC) GOMEMLIMIT=$(NILAWAY_GOMEMLIMIT) GOFLAGS=-tags=$(ANALYSIS_TAGS) $(NILAWAY) -include-pkgs=opendustpan.org/opendustpan ./...

vuln: ## Known vulnerabilities in the code paths we call (reads the database at vuln.go.dev).
	$(GOVULNCHECK) -tags $(ANALYSIS_TAGS) ./...

# The rule packs come from the Semgrep registry on each run. No metrics are sent.
# use-of-md5 is excluded: gosec reports the same lines, and each one has a
# nolint with its reason.
#
# The second scan runs the local rules in .semgrep. It ignores .semgrepignore
# files and the Semgrep default ignores, which skip test files, because the
# rules in .semgrep are about test files.
#
# The third command runs the test cases of the rule
# tests-no-wall-clock-assertion: each ruleid line must match and each ok line
# must not.
semgrep: ## Semgrep registry rules for Go, the Dockerfile, the workflows and secrets.
	semgrep scan --error --metrics=off --disable-version-check --quiet \
		--config p/golang --config p/dockerfile --config p/github-actions --config p/secrets \
		--exclude-rule go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5 .
	semgrep scan --error --metrics=off --disable-version-check --quiet \
		--x-ignore-semgrepignore-files --config .semgrep .
	semgrep --test --metrics=off --disable-version-check \
		--config .semgrep/tests-no-wall-clock-assertion.yml .semgrep/tests-no-wall-clock-assertion.go

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

# Scans the commits that HEAD reaches, not the uncommitted files. Each
# candidate is sent to its provider for verification. "unknown" is a candidate
# that could not be verified (for example, the provider did not answer).
# Exit code 1 means a verified secret, or an unknown result after three runs.
# .trufflehog-exclude-paths.txt lists files that are known false positives,
# one regular expression per line. Each entry needs the owner's approval.
# Each entry stays only because an old commit holds a fake credential in that
# path. A test builds a fake credential with internal/fakecred, and a raw one
# carries a trufflehog:ignore tag with a reason. Do not add a path.
# Scan only the history reachable from HEAD, so a commit on another branch cannot fail this scan.
# An unknown result gets up to two more runs, and a verified result fails at once.
trufflehog: ## TruffleHog: verified secrets in the Git history.
	python3 -I scripts/trufflehog-gate.py --selftest
	python3 -I scripts/trufflehog-gate.py scan -- trufflehog git file://. --branch=HEAD --results=verified,unknown --no-update --exclude-paths=.trufflehog-exclude-paths.txt

# The same platform pairs as scripts/release-build.sh. The release archives
# and the container image ship these files (third_party_licenses/).
RELEASE_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

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
ADR_DECISIONS := python3 -I scripts/adr-decisions.py

# The change links between ADRs and the generated docs/adr/INDEX.md. ADR 296
# is the first ADR with a "Changes:" list. The older ADRs have only the
# "Changed by:" items of their links.
ADR_INDEX := python3 -I scripts/adr-index.py --changes-from 296

docs-lint: ## ASD-STE100 structural rules, banned words and the Repowise decisions file of the ADRs, for README, CLAUDE.md, GLOSSARY.md, the community files, the guides, the reference, the explanations and ADRs from 14 on.
	$(STE_LINT) $(DOCS) $(DOCS_ADRS)
	$(BANNED) --selftest
	$(BANNED) $(DOCS) $(DOCS_ADRS)
	$(ADR_DECISIONS) --selftest
	$(ADR_DECISIONS) --check
	python3 -I scripts/adr-index.py --selftest
	$(ADR_INDEX) --check
	@if grep -rn '^TBD' openspec/specs; then echo 'openspec/specs: a Purpose is a placeholder. Write a real Purpose.'; exit 1; fi

adr-decisions: ## Write .repowise/decisions.yaml, the accepted decisions for Repowise, from docs/adr. docs-lint fails when the file is not current.
	$(ADR_DECISIONS)

# With no VERSION there is no tag to check, so the target runs the cases that
# exist without one: the script against an empty record, a record of another
# version, a record with a CR or a NUL byte, and a missing record. With
# VERSION=vX.Y.Z on the command line it checks the record for that tag, as a
# release does. A VERSION in the environment does not change the mode.
release-record-check: ## Check the release record: the cases that need no tag, or VERSION=vX.Y.Z for a tag.
ifeq ($(origin VERSION),command line)
	./scripts/check-release-record.sh '$(VERSION)'
else
	go test ./internal/releaserecord -run 'TestCheckReleaseRecordRefuses|TestReleaseBuildChecksTheRecordFirst|TestReleaseRecordTargetMode' -count=1
endif

adr-index: ## Write docs/adr/INDEX.md from the change links of the ADRs.
	$(ADR_INDEX)

chart-lint: ## Lint and render the Helm chart (helm lint, helm template, kubeconform). Needs helm and kubeconform; not part of check.
	./scripts/chart-lint.sh

# The store packages run for more than 10 minutes, the default limit of go test,
# on a CI runner with 4 cores. GO_TEST_TIMEOUT is the limit for each package.
GO_TEST_TIMEOUT := 20m

# TEST_TIMEOUT is the limit for each package in test, test-nocache,
# test-postgres, journal-test, bench and test-s3: each target that runs whole
# packages. A target that runs one named test keeps the go test default. The
# first three targets run the store packages together with all other
# packages. In a session container with 4 CPUs and 15 GB, pkg/dustpan took
# 1371 s, pkg/store/sqlite 1721 s and pkg/store/postgres 1405 s, each one
# alone, so 20m failed every run. The CI targets keep GO_TEST_TIMEOUT and
# STORE_TEST_TIMEOUT. Set TEST_TIMEOUT to change the limit.
TEST_TIMEOUT ?= 45m

# With -race the compiler also adds checkptr checks to every package. In the
# SQLite engine, which is C translated to Go with much unsafe pointer
# arithmetic, these checks took about 30% of the CPU of the SQLite store
# tests. RACE_TEST_FLAGS turns checkptr off for modernc.org/... only: the race
# detector stays on for all code, and checkptr stays on for our own code. On
# 4 cores, the dev data suite of pkg/store/sqlite used 904 s of user CPU with
# the checks and 647 s without them. The cost: checkptr does not find unsafe
# pointer misuse inside those modules, which we do not maintain. A second
# -gcflags on the same command line replaces this one, so a target that needs
# its own -gcflags must merge them into one.
RACE_TEST_FLAGS := -race -gcflags='modernc.org/...=-d=checkptr=0'

test: ## Go tests; the PostgreSQL suite is skipped unless DUSTPAN_TEST_POSTGRES_URL is set. A package whose code, files and environment did not change gives its cached result.
	go test $(RACE_TEST_FLAGS) -timeout $(TEST_TIMEOUT) ./...

test-nocache: ## Go tests as in test, but every package runs again. CI does the same on main every 2 hours, with test-ci-other and test-ci-store, to find flaky tests.
	go test $(RACE_TEST_FLAGS) -count=1 -timeout $(TEST_TIMEOUT) ./...

# CI runs the two store packages in jobs of their own, one for each package, so
# that they do not share the 4 cores of a runner with each other or with the
# other packages. GO_TEST_COUNT=-count=1 runs every package again, as the
# schedule of CI does.
STORE_TEST_PKGS := opendustpan.org/opendustpan/pkg/store/sqlite opendustpan.org/opendustpan/pkg/store/postgres
GO_TEST_COUNT ?=

# STORE_TEST_TIMEOUT is the limit of test-ci-store only, sized for one store
# package on a CI runner: pkg/store/postgres alone took more than 20 minutes
# there. It is not a fix of that time, and GO_TEST_TIMEOUT stays the limit of
# test-ci-other.
STORE_TEST_TIMEOUT := 30m

test-ci-other: ## Go tests as in test, without the two store packages; the go job of CI runs it.
	go list ./... >/dev/null
	go test $(RACE_TEST_FLAGS) $(GO_TEST_COUNT) -timeout $(GO_TEST_TIMEOUT) $$(go list ./... | grep -v -x -F $(addprefix -e ,$(STORE_TEST_PKGS)))

# COVER_FLAGS: a fast coverage pass has no -race and no -count. Set mode
# counts a block once, and that is enough for a percentage. Atomic mode is
# needed only with -race.
COVER_FLAGS ?= -covermode=set

cover: ## Fast coverage pass for the packages of test-ci-other, without -race: writes coverage.out and prints a table, also when a test fails. The exit status is the one of go test. In CI, GITHUB_STEP_SUMMARY gets the same table.
	go list ./... >/dev/null
	python3 -I scripts/cover-summary.py --selftest
	status=0; \
	go test $(COVER_FLAGS) -coverprofile=coverage.out -timeout $(GO_TEST_TIMEOUT) $$(go list ./... | grep -v -x -F $(addprefix -e ,$(STORE_TEST_PKGS))) || status=$$?; \
	python3 -I scripts/cover-summary.py coverage.out || test $$status -ne 0 || status=1; \
	exit $$status

test-ci-store: ## Go tests of one store package, STORE=sqlite or STORE=postgres; each store job of CI runs it.
	@case "$(STORE)" in sqlite|postgres) ;; *) echo "set STORE=sqlite or STORE=postgres"; exit 1 ;; esac
	go test $(RACE_TEST_FLAGS) $(GO_TEST_COUNT) -timeout $(STORE_TEST_TIMEOUT) ./pkg/store/$(STORE)/

test-postgres: ## Go tests including PostgreSQL (needs DUSTPAN_TEST_POSTGRES_URL).
	@test -n "$$DUSTPAN_TEST_POSTGRES_URL" || (echo "set DUSTPAN_TEST_POSTGRES_URL" && exit 1)
	go test $(RACE_TEST_FLAGS) -count=1 -timeout $(TEST_TIMEOUT) ./pkg/store/postgres/...

bench: ## Fast benchmarks (delta apply, gzip, checkpoint store) with one iteration each; proves they compile and run.
	go test -run '^$$' -bench . -benchtime 1x -timeout $(TEST_TIMEOUT) ./pkg/stack/ ./pkg/store/sqlite/

load: ## Run the load harness against a self-started server and write load-report.json (SQLite default; set DUSTPAN_TEST_POSTGRES_URL for PostgreSQL). Not part of check.
	go run ./load -out load-report.json

bench-s3: ## S3 latency benchmark: MinIO, Garage, PostgreSQL 18 and Toxiproxy in containers pinned by digest (needs Docker; BENCH_S3_ARGS, BENCH_S3_OUT; see docs/reference/s3-latency-benchmark.md). Not part of check.
	scripts/bench-s3.sh

bench-s3-protocol: ## Protocol run of the blob families: old and new path, 10,000 operations and 5 alternating runs each (needs Docker; see docs/reference/blob-write-benchmark-record.md). Not part of check.
	BENCH_S3_MODE=protocol scripts/bench-s3.sh

test-s3: ## Go tests in pkg/blob/s3 against a live S3-compatible server (needs DUSTPAN_TEST_S3_*).
	@test -n "$$DUSTPAN_TEST_S3_ENDPOINT" && test -n "$$DUSTPAN_TEST_S3_BUCKET" && test -n "$$DUSTPAN_TEST_S3_ACCESS_KEY" && test -n "$$DUSTPAN_TEST_S3_SECRET_KEY" || (echo "set DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, DUSTPAN_TEST_S3_ACCESS_KEY and DUSTPAN_TEST_S3_SECRET_KEY" && exit 1)
	go test $(RACE_TEST_FLAGS) -count=1 -timeout $(TEST_TIMEOUT) ./pkg/blob/s3/...

e2e: ## Drive real Pulumi CLI and OpenTofu versions against the server (downloads CLIs; see e2e/doc.go). Not part of check; CI runs it as its own job.
	go test -tags e2e $(RACE_TEST_FLAGS) -count=1 -parallel 16 -timeout 60m ./e2e/...

e2e-ha: ## Drive three server binaries of one installation behind a round-robin proxy on PostgreSQL (needs DUSTPAN_TEST_POSTGRES_URL; E2E_HA_BACKUP_MB sets the backup size; see e2e/doc.go). Not part of check; the e2e-ha-nightly workflow (e2e/e2e-ha-nightly.workflow.yml) runs it.
	E2E_INSTANCES=3 go test -tags e2e $(RACE_TEST_FLAGS) -count=1 -parallel 16 -timeout 60m -run '^TestHighAvailability$$' ./e2e/

CLIENTGEN := go run ./cmd/clientgen -target typescript -description internal/api/apidesc/openapi.json -out clients/typescript

clients-generate: ## Regenerate the TypeScript client package in clients/typescript from the API description.
	$(CLIENTGEN)

clients-check: ## Fail when clients/typescript is not what a fresh run of the generator gives (like sqlc diff). Needs Go only.
	$(CLIENTGEN) -check

clients-ts-test: ## Compile the TypeScript client strictly and run its tests (needs tsc and Node 22). Not part of check.
	cd clients/typescript && tsc -p tsconfig.json && node --test "test/*.test.mjs"

clients-conformance: clients-ts-test ## Build the server, generate the Go and TypeScript clients from the description that the server serves, and run the client conformance suite on them. Not part of check.
	DUSTPAN_TEST_CLIENTGEN_CONFORMANCE=1 go test $(RACE_TEST_FLAGS) -count=1 -timeout 25m -run 'TestGoClientConformance|TestTypeScriptClientConformance' ./internal/clientgen/conformance/

checks-agent: ## Sync the branch with main, then run generate, check, e2e and a TruffleHog scan of the branch commits with cleanup (SYNC=merge|rebase|none, SKIP="steps", CLEAN_CACHE=1 also deletes the e2e CLI cache).
	./scripts/checks-agent.sh

# With TOXIPROXY_URL set, the target uses that Toxiproxy server (CI runs it
# as a service container) and downloads no binary.
faults: ## Run Toxiproxy network fault tests against a self-started server (downloads toxiproxy-server unless TOXIPROXY_URL is set; test-only, never ships). Not part of check.
	if [ -z "$$TOXIPROXY_URL" ]; then TOXIPROXY_BIN="$$(./scripts/toxiproxy.sh)" && export TOXIPROXY_BIN; fi && \
	go test -tags faults $(RACE_TEST_FLAGS) -count=1 -timeout 20m ./e2e/faults/...

e2e-journal: ## Drive the real Pulumi CLI through the journal route on the recording build (ADR 43, work item 9). Not part of check.
	E2E_PULUMI_VERSIONS=3.268.0 go test -tags "e2e journalrecording" $(RACE_TEST_FLAGS) -count=1 -run 'TestPulumiJournalingOn|TestPulumiJournalRefusalCancel|TestPulumiJournalingOnVersion|TestPulumiJournalStateMigration' -timeout 60m ./e2e/...

upgrade-test: ## Restart the current binary on the same database and check data, secrets and the held lock survive (downloads CLIs; empty-database case without a v* tag, previous-tag case with one). Not part of check.
	E2E_SERVER=binary go test -tags "e2e upgrade" $(RACE_TEST_FLAGS) -count=1 -v -timeout 20m ./e2e/ -run TestUpgrade

mixed-version-test: ## Serve the current binary and the binary of each of the last 2 v* tags on one database and drive the real Pulumi CLI and OpenTofu through both (downloads CLIs; a second current process without a v* tag). Not part of check.
	E2E_SERVER=binary go test -tags "e2e upgrade" $(RACE_TEST_FLAGS) -count=1 -v -timeout 30m ./e2e/ -run 'TestMixedVersion'

crash-test: ## Stop the server binary with SIGKILL during a Pulumi checkpoint save, an OpenTofu upload and a lock, then check recovery on the same database and blobs (downloads CLIs; PostgreSQL with DUSTPAN_TEST_POSTGRES_URL). Not part of check.
	E2E_SERVER=binary go test -tags e2e $(RACE_TEST_FLAGS) -count=1 -v -timeout 20m ./e2e/ -run TestBinaryCrash

SOAK_DURATION ?= 30m
SOAK_PULUMI ?= 4
SOAK_TOFU ?= 4
SOAK_REPORT_DIR ?= soak-reports

CONSOLE_REPO ?= https://github.com/OpenDustpan/console
CONSOLE_REF ?= b56450ef862edfbe90af91e62117e22bea3ff0c5
DEMO_PORT ?= 8080

soak: ## Run concurrent real Pulumi CLI and OpenTofu processes against one server binary for SOAK_DURATION (default 30m; SOAK_DURATION=60s for a smoke check), with SOAK_PULUMI and SOAK_TOFU workers each (default 4) writing CSV and Markdown reports to SOAK_REPORT_DIR (SQLite default; PostgreSQL with DUSTPAN_TEST_POSTGRES_URL). Not part of check; see docs/guides/soak.md.
	SOAK_DURATION=$(SOAK_DURATION) SOAK_PULUMI=$(SOAK_PULUMI) SOAK_TOFU=$(SOAK_TOFU) SOAK_REPORT_DIR=$(SOAK_REPORT_DIR) E2E_SERVER=binary go test -tags "e2e soak" $(RACE_TEST_FLAGS) -count=1 -v -timeout 60m ./e2e/ -run TestSoak

BASELINE_REF ?= origin/main

regenerate-baseline: ## Write the baseline migration of each dialect from the migrations of BASELINE_REF (default origin/main); needs DUSTPAN_TEST_POSTGRES_URL and pg_dump. Not part of check.
	./scripts/regenerate-baseline.sh $(BASELINE_REF)

zones-generate: ## Regenerate pkg/preferences/zones_gen.go, the list of zone names, from the time zone archive of the Go toolchain. Run it after a Go upgrade.
	go run ./cmd/zonegen -out pkg/preferences/zones_gen.go

generate: zones-generate ## Regenerate sqlc query code after editing queries.sql or migrations, the zone list after a Go upgrade, and the settings reference after editing the CLI declarations.
	sqlc generate
	$(CLIENTGEN)
	go test ./internal/cli/ -run TestConfigurationReference -update -count=1
	go test ./internal/pulumijournal/internal/gen/ -run TestGenerateMatchesCommittedFile -update -count=1

journal-test: ## Journal route tests of the recording build. No CLI download; part of check.
	go test -tags journalrecording $(RACE_TEST_FLAGS) -count=1 -timeout $(TEST_TIMEOUT) ./internal/api/pulumi/ ./pkg/dustpan/

journal-guard: ## Refuse a release build that uses the journalrecording tag (ADR 43, work item 9).
	@if grep -rq journalrecording Dockerfile .github/workflows scripts 2>/dev/null || \
		sed -n '/^build:/,/^[a-z-]*:/p' Makefile | grep -q journalrecording; then \
		echo "journal-guard: a release path uses the journalrecording tag. Remove it."; exit 1; \
	fi

build: ## Build the server binary.
	go build -ldflags "-X main.version=$$(git describe --tags --always --dirty)" -o bin/opendustpan ./cmd/opendustpan

build-launcher: ## Build the launcher binary, static with no cgo.
	CGO_ENABLED=0 go build -ldflags "-X main.version=$$(git describe --tags --always --dirty)" -o bin/dustpan-launcher ./cmd/dustpan-launcher

demo: build ## Run the server with the web console and demo data on 127.0.0.1:8080 (needs Go, Node and npm; not part of check).
	CONSOLE_REPO="$(CONSOLE_REPO)" CONSOLE_REF="$(CONSOLE_REF)" DEMO_PORT="$(DEMO_PORT)" ./scripts/demo.sh

image-smoke: ## Build the container image and run the local smoke test (needs docker; not part of check).
	./scripts/smoke-image.sh

compose-smoke: ## Start the stack of deploy/compose and drive the real Pulumi CLI and OpenTofu against it (needs docker with compose and openssl; not part of check).
	E2E_COMPOSE=1 go test -tags e2e $(RACE_TEST_FLAGS) -count=1 -timeout 30m -run '^TestComposeQuickstart$$' ./e2e/...
