# 76. AI-assisted contributions

Status: accepted · 2026-10-17 · the sign-off of agent commits after the push of October 2026 is an open question

Changed by:

- ADR 317 changes decisions 7 and 8: the identity of an agent commit and the trailer.

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

The owner did not decide some details of decisions 6 and 7. The PM session
added them on 2026-10-17. Each one has the mark "(PM addition)".

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
   review (PM addition). The maintainers decide what counts as bulk (PM
   addition).
7. **The project's own agents follow the same rules.** The `Co-Authored-By`
   trailer is the disclosure (PM addition). Each agent pull request pastes
   the output of its checks (PM addition).
8. **Agent sign-off during the current push.** The owner decided on
   2026-10-17: until the push ends on 2026-10-22, each agent commit carries
   a `Signed-off-by:` line with the commit author identity of the agent. The
   owner cleans up the history later. Nothing waits for this cleanup.

## Open question

Who signs off an agent commit after the current push? A `Signed-off-by:`
line is a personal legal statement, and only the person can give it. The
owner decides. One proposal is that the person who runs the agent session
signs off.

## Consequences

- `CONTRIBUTING.md` adds the sign-off, the `make check` rule and the bulk
  rule, and drops the sentence that limits the rules to outside
  contributions.
- `CLAUDE.md` and `CONTRIBUTING.md` tell an agent to paste the output of its
  checks into each pull request that it opens (decision 7).
- A DCO check in CI can come later. Until then, reviewers check the
  sign-off.
- The rule on AI-generated media in `CONTRIBUTING.md` stays.
