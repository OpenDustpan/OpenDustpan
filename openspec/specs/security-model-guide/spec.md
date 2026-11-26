# security-model-guide Specification

## Purpose
The guide for break-glass accounts tells a site administrator the floor of working accounts and the guard.

## Requirements

### Requirement: guide-break-glass

Where the guide covers break-glass accounts, the documentation SHALL state the setup rule, the guard and the upgrade path.

#### Scenario: Operator reads the break-glass section
- **WHEN** an operator opens `docs/guides/break-glass-accounts.md`
- **THEN** the page states the two-account setup, the guard and the upgrade path, with glossary terms and no internal reference
