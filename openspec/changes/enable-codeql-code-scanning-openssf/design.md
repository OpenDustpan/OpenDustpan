## Context

Semgrep stays the required SAST gate. CodeQL and Scorecard add advisory layers. CodeQL starts advisory-only until triaged. Scorecard publish stays off until the repo is public and the owner approves.

## Decisions

1. **Pinned and gated workflows.** Add NEW `.github/workflows/codeql.yml` and NEW `.github/workflows/scorecard.yml` pinned to full 40-character commit SHAs. Each job declares least-privilege permissions. A job condition runs the SARIF upload only on a public repo or with GHAS, and never on a fork pull request. CodeQL uses autobuild with the security-extended queries. Scorecard sets publish_results to false and runs on schedule only.
2. **Dependabot covers the pins.** Add NEW `.github/dependabot.yml` for the github-actions ecosystem with a weekly interval, so the pins update. Add NEW `scripts/check-scanning-workflows.sh` to check pins, permissions, triggers, and gates.

## Structure

No Go type changes. Only NEW workflow files, NEW dependabot file, NEW check script, and NEW docs note change.

## Risks

- SARIF upload fails on a private repo without the gate, and each push turns red.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | CI-only change with no product latency, storage or rate effect |  |  |
