# Lead workflow: how work items run

These rules are for the lead session, the session that assigns work and
merges pull requests. A worker session follows its brief and ignores this
file. The project owner set these rules. Only the owner changes them.

## Who does the work

1. Run each work item in its own worker session. Start the worker sessions
   in parallel.
2. A worker session uses a small, low-cost Claude model. It does not write
   code. It drives the low-cost implementation model in OpenCode with
   `opencode run`, and it checks the result.
3. Give each worker the brief in `docs/agents/worker-brief.md`. Fill in
   every placeholder. The brief tells the worker each step. Do not let a
   worker improvise.
4. Archive each worker session when the worker completes its work item.
5. Do not give a new work item to a session that uses a large model. Such a
   session only finishes its open pull requests. Then archive it.
6. Do not run implementation or review work in your own subagents. A
   subagent stops when the owner interrupts the lead, and it costs too much.
   Put review work in a worker session too.

Reason: the implementation model costs about $0.20 for each million output
tokens. This cost is what makes the project possible. Use Claude tokens for
coordination, not for code.

## Merging

1. Merge without waiting for CI when all of these are true:
   - The worker reports a full `make check` with PostgreSQL and `make e2e`
     on the exact head commit.
   - The review is complete.
   - When main changed the same files, a test of the pull request merged
     with current main passes.
2. Before each merge, read the pull request body for closing keywords
   ("Fixes", "Closes"). Remove a keyword that closes the wrong issue.
3. Only the lead merges. A worker never merges.
