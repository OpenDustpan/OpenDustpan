# 76. AI-assisted contributions

Status: accepted · 2026-10-17 · decision 7, the sign-off of agent commits, waits for confirmation

## Context

`CONTRIBUTING.md` already lets contributors use AI tools. It asks them to
say so, to understand every change, and to edit what the tool writes. It
says that these rules are for outside contributions only. It does not ask
for a sign-off, and it says nothing about bulk pull requests.

Agent sessions do most of the maintainer work on OpenDustpan. Each commit
that an agent writes carries a `Co-Authored-By` trailer.

The project owner decided: AI-assisted contributions are welcome, with
disclosure, the DCO sign-off, full understanding, and `make check` passing.
The project closes bulk AI pull requests. The project's own agents follow
the same rules.

## Decision

1. **AI-assisted contributions are welcome**, under the rules below.
2. **Disclosure.** The pull request names each AI tool and says which parts
   it wrote.
3. **DCO sign-off.** Each commit carries a `Signed-off-by:` line (`git commit
   -s`). The line certifies the Developer Certificate of Origin 1.1 for the
   commit.
4. **Full understanding.** The person who submits the change understands all
   of it. That person can explain what it does and how it works with the
   rest of the system, without the tool.
5. **`make check` passes** before the pull request asks for a review.
6. **The project closes bulk AI pull requests** with no review. An example
   is many pull requests that a tool opened with no issue and no human
   review. The maintainers decide what counts as bulk.
7. **The project's own agents follow the same rules.** The `Co-Authored-By`
   trailer is the disclosure. Each agent pull request pastes the output of
   its checks. The sign-off of an agent commit names the person who runs the
   agent session (proposal, waits for confirmation).

## Consequences

- `CONTRIBUTING.md` adds the sign-off, the `make check` rule and the bulk
  rule, and drops the sentence that limits the rules to outside
  contributions.
- A DCO check in CI can come later. Until then, reviewers check the
  sign-off.
- The rule on AI-generated media in `CONTRIBUTING.md` stays.
