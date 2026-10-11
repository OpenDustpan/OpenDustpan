# Local gates. CI runs the same checks (.github/workflows/ci.yml).
# Tool versions are pinned here and in CI; install the matching binaries:
#   golangci-lint v2.14.0  https://golangci-lint.run/welcome/install/
#   sqlc          v1.31.1  https://docs.sqlc.dev/en/latest/overview/install.html
#   semgrep       1.178.0  https://semgrep.dev/docs/getting-started/quickstart
#   python3       any 3.x, standard library only (for docs-lint)
# nilaway and govulncheck need no install: go run builds the pinned versions below.

.PHONY: check fmt lint nilaway vuln semgrep docs-lint test test-postgres e2e upgrade-test generate build image-smoke

check: lint nilaway vuln semgrep docs-lint test ## Everything CI checks.

fmt: ## Format Go code (gofumpt, gci, golines).
	golangci-lint fmt ./...

lint: ## Strict lint, tidy and generated-code checks for Go.
	go mod tidy -diff
	sqlc diff
	golangci-lint run ./...

# The same build tags as .golangci.yml, so the e2e and upgrade packages are checked too.
ANALYSIS_TAGS := e2e,upgrade
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

# Prose that ADR 14 holds to ASD-STE100. Accepted ADRs before ADR 14 are exempt:
# ADR 1 forbids editing them.
STE_LINT  := python3 -I .agents/skills/asd-ste100/scripts/ste-lint.py
DOCS      := $(wildcard README.md CLAUDE.md GLOSSARY.md)
DOCS_ADRS := $(shell ls docs/adr/*.md | awk -F/ 'substr($$3, 1, 4) + 0 >= 14')

docs-lint: ## ASD-STE100 structural rules for README, CLAUDE.md, GLOSSARY.md and ADRs from 14 on.
	$(STE_LINT) $(DOCS) $(DOCS_ADRS)

test: ## Go tests; the PostgreSQL suite is skipped unless DUSTPAN_TEST_POSTGRES_URL is set.
	go test -race -count=1 ./...

test-postgres: ## Go tests including PostgreSQL (needs DUSTPAN_TEST_POSTGRES_URL).
	@test -n "$$DUSTPAN_TEST_POSTGRES_URL" || (echo "set DUSTPAN_TEST_POSTGRES_URL" && exit 1)
	go test -race -count=1 ./pkg/store/postgres/...

e2e: ## Drive real Pulumi CLI and OpenTofu versions against the server (downloads CLIs; see e2e/doc.go). Not part of check; CI runs it as its own job.
	go test -tags e2e -race -count=1 -parallel 16 -timeout 60m ./e2e/...

upgrade-test: ## Restart the current binary on the same database and check data, secrets and the held lock survive (downloads CLIs; empty-database case while no v* tag exists). Not part of check.
	E2E_SERVER=binary go test -tags "e2e upgrade" -race -count=1 -v -timeout 20m ./e2e/ -run TestUpgrade

generate: ## Regenerate sqlc query code after editing queries.sql or migrations.
	sqlc generate

build: ## Build the server binary.
	go build -ldflags "-X main.version=$$(git describe --tags --always --dirty)" -o bin/opendustpan ./cmd/opendustpan

image-smoke: ## Build the container image and run the local smoke test (needs docker; not part of check).
	./scripts/smoke-image.sh
