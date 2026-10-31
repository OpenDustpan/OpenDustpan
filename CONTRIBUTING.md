# Contributing to OpenDustpan

Thank you for your time. This file says how a change gets into OpenDustpan.
`CLAUDE.md` holds the full rules for code, tests and text. Read it before
your first change.

## Open an issue first

Open an issue before you write a feature or a change to an API. Say what you
want to do and why. The project owner decides whether the change fits, and a
short talk first can save you a pull request that the project cannot take.

A typo fix, a broken link or a small bug fix with a test needs no issue.

A change to the architecture starts as an RFC in `docs/rfc/`.
`docs/rfc/README.md` says how to write one.

## Make the change

1. Fork the repository and make a branch.
2. Make one change per pull request. Keep it small enough to review.
3. Add a test for each new rule. Each new test package needs a
   `leak_test.go` file, as `CLAUDE.md` says.
4. Use the words in `GLOSSARY.md`. Write docs, errors and messages in the
   style that `CLAUDE.md` sets.
5. Do not edit `docs/reference/configuration.md` by hand. The setting texts
   are in `internal/cli/settings.go`. The prose of the page is in
   `internal/cli/reference.go`. Run `make generate` after a change to either
   file. `TestConfigurationReference` fails on a hand edit of the page.

Ask in the issue before you add a dependency.

## Check the change

Run the same gates as CI:

```sh
make check
```

`make check` must pass with zero findings. The `Makefile` lists the tool
versions to install. Set `DUSTPAN_TEST_POSTGRES_URL` to include the
PostgreSQL tests.

With the race detector, one run of `pkg/store/sqlite` or `pkg/store/postgres`
takes more than 10 minutes, the default limit of `go test`. `make test` and
`make test-ci-store` set a longer limit. When you run a store package with
`go test -race` directly, pass `-timeout 60m`.

A change to an endpoint must also pass the end-to-end suite, which drives
the real Pulumi CLI and OpenTofu against the server:

```sh
make e2e
```

The suite downloads the CLI versions first. `e2e/doc.go` lists its
settings. CI runs the same checks and the end-to-end suite on each pull
request.

## Open the pull request

Say what changed and why, and link the issue. Say how you tested the change.
A reviewer can ask for changes. When CI is green and the review passes, the
project owner decides whether it merges.

## Documentation

A change that a user can see includes its page in the user documentation,
in the same pull request. The user documentation is `README.md`,
`docs/guides/`, `docs/reference/` and `docs/explanation/`.

The author of a change owns the pages that the change adds or edits. The
author makes sure that each of these pages agrees with the behavior that
the change gives.

One maintainer owns the structure and the voice of the documentation: which
pages exist, how they link to each other, and the style that `CLAUDE.md`
sets. Ask in the issue before you add, move or remove a page.

## Governance

The project owner, [@DwaineSaunderson](https://github.com/DwaineSaunderson),
maintains OpenDustpan and decides what merges. Accepted decisions are in
`docs/adr/`, and open questions are RFCs in `docs/rfc/`. A change that goes
against an accepted decision starts as an RFC.

## Use of AI tools

You can use AI tools to help you write code, tests and text. These rules
apply:

- **Say that you used AI.** In the pull request or the issue, name the tool
  and say which parts it wrote.
- **Understand every change.** A human submits the change, understands all
  of it, and can explain what it does and how it works with the rest of the
  system, without the help of the tool. If you cannot do this, do not submit
  the change.
- **Edit what the tool writes.** Issues, comments and pull request text that
  a tool helped to write need a human review and edit before you post them.
  Remove the noise.
- **Sign off each commit.** Use `git commit -s`. The `Signed-off-by:` line
  certifies the Developer Certificate of Origin 1.1 for the commit.
- **Pass `make check`** before you ask for a review.
- **No bulk pull requests.** The project closes bulk AI pull requests with
  no review, for example many pull requests that a tool opened with no issue
  and no human review.
- **No AI-generated media.** Do not submit images, video or audio that an AI
  tool made. Text and code are the only AI-generated content that the
  project accepts, under the rules above.

These rules follow the
[AI usage policy of Ghostty](https://github.com/ghostty-org/ghostty/blob/main/AI_POLICY.md).

### How the project itself uses AI

Agent sessions do most of the maintainer work on OpenDustpan. Each commit
that an agent writes on its pull request branch carries a `Co-Authored-By`
trailer. The trailer names the agent tool. It never names a model or a model
version. Do not put a model name in a commit message, a pull request title or
a pull request body. Every change passes CI, a security review and a review
by a second session before a merge. The rules above also apply to these
agents. The `Co-Authored-By` trailer is their disclosure. Until 2026-10-22,
an agent signs off each commit
with its own commit author identity. The project owner decides the rule for
agent sign-off after that date. An agent pastes the output of its checks into
the description of each pull request that it opens.

## Conduct and security

All contributors follow the [code of conduct](CODE_OF_CONDUCT.md). Report a
security problem in private, as [SECURITY.md](SECURITY.md) says. Do not open a
public issue for it.

## License

OpenDustpan is under the [Apache License 2.0](LICENSE). Section 5 of the
license applies to your contribution: what you submit is under the same
license, unless you say otherwise.

OpenDustpan has no contributor license agreement and no copyright assignment.
You keep the copyright of your contribution. The project cannot relicense your
code without your agreement.
