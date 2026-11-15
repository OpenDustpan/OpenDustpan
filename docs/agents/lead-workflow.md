# OpenDustpan lead: owner's standing rules (do not drop on summary)

These are the project owner's own instructions. They hold until the owner changes them.

## Who does the work

- Implement every work item in parallel through a **Haiku Claude Code session** that drives **Muse 1.3 Contributor** in
  OpenCode (`opencode run -m opencode-go/muse-spark-1.3-contributor`). One session per work item (or a pair).
- At session start, read `docs/agents/lead-workflow.md` on main (#783 merged). Brief each Haiku session with
  `docs/agents/worker-brief.md` on main. Placeholder values (no `<TRAILERS>`: workers add no commit trailers, owner
  2026-10-16): `<LEAD_SESSION>` = the ID of the lead's own session;
  `<LEAD_SESSION_URL>` = the URL of the lead's own session; `<IMPLEMENTATION_MODEL>` =
  opencode-go/muse-spark-1.3-contributor. Create sessions with model claude-haiku-5-5, tag opendustpan-swarm. Haiku
  follows steps; it does not improvise. The environment config sets GOTOOLCHAIN=go1.27.4 (owner, 2026-10-16), so new
  sessions need no prompt line for it. If make nilaway or make vuln fails on a toolchain mismatch, tell the worker to
  `export GOTOOLCHAIN=go1.27.4`.
- **Kill (archive) each Haiku session when its work item is finished.**
- A worker that refuses the lead's direction is fired (owner, 2026-10-17): archive it and start a new worker with the full task in its first prompt. Do not argue with it, and do not ask the owner to step in. Put every change of direction in a new session's first prompt, not in a message to a running session.
- **Do not run implementation or review work in my own subagents (the Agent tool).** They die when the owner interrupts
  me, and they are expensive. Put reviews into sessions too.
- Why: Muse is a much cheaper model than Claude per output token. That is what makes the project financially viable.
  Spend Claude tokens on coordination, not on code.
- Expensive models (owner, 2026-10-16): a Sonnet or Opus session is allowed with discretion for architecture or security
  review, research (for example academic literature), design, or a task the cheap path failed twice. Tell the owner the
  reason and the expected cost first, and archive the session when it is done.

- PostgreSQL 18 runs natively in every new session (owner's setup script, 2026-10-16) on localhost:5432, and the
  environment sets DUSTPAN_TEST_POSTGRES_URL. The server is not running when a session starts (the setup script runs at
  image build), so every worker prompt says: if `psql "$DUSTPAN_TEST_POSTGRES_URL" -Atc "select 1"` is refused, run
  `service postgresql start` and check again. No Docker steps. Sessions started before 2026-10-16 22:30 UTC still need
  the old Docker recipe (mirror.gcr.io/library/postgres:18, --shm-size=1g, port 55432).
- Keep the pipeline full (owner, 2026-10-16). On every wake: list open ready-for-agent issues and open PRs in BOTH
  repositories, OpenDustpan/OpenDustpan and OpenDustpan/console (owner, 2026-10-17); every unblocked issue and every
  open PR without a live worker gets a Haiku worker now. Do not wait to be asked.

- Swarm watchdog (owner, 2026-10-16): an hourly scheduled task starts a fresh Haiku session that reports to me. When its
  report arrives, act on it, then archive that watchdog session at once.

- mergiraf is installed in all new sessions (owner, 2026-10-16). It resolves many list and import conflicts on merge,
  but its result can be wrong: it once kept a migration twice in schema.go. A worker still runs make generate and the
  local gates after every merge. Workers started before this date may not have it.

- Repowise (owner, 2026-10-17): every session has the read-only Repowise tools (mcp__Repowise_OpenDustpan__*; see `docs/agents/local-setup.md` for the local setup) over both
  repositories. Worker and reviewer prompts say: use get_context, search_codebase and get_symbol to find code before
  grepping, and get_blast_radius before changing a public function, a store method or an API route; say in the final
  report whether it helped. I use get_blast_radius and get_change_risk before merges. Trial (owner, 2026-10-17): 10
  days, ends 2026-10-27. Log each worker's and reviewer's answer, and each time a Repowise result changed one of my
  merge decisions, in repowise-log.md in the scratchpad directory of the session (date,
  session or PR, tool, helped yes/no, what it found that grep would not). On 2026-10-26, give the owner a verdict from
  that log: keep or drop.

## Session tracking (owner, 2026-10-17: "I do need a fix for you forgetting about sessions")

- The live session list is the record, not my memory. On every wake and every check-in: list_sessions (mine: true, limit
  100), then run
  `python3 docs/agents/lead_sessions.py <saved result file>`.
  Act at once on every BLOCKED or NEEDS line (send_message with priority now). Give every session that is idle for more
  than 30 minutes its next step, or archive it.
- Unattended workers can wait forever on a permission prompt or an unsent message. The NEEDS line shows these. Give the
  worker another way that is allowed (opencode or the Edit tool instead of sed). If the step needs the owner's own
  approval in that session, tell the owner the session URL.
- Keep each session title as "<role>: PR N (state)" or "<role>: #N (state)". Rename a session when its work changes.
- Check-in (owner, 2026-10-17): scheduled task trig_0197R4UtyxPRKvTpmfzPAySj fires into this session at :15 every hour.
  Its steps are in docs/agents/lead-checkin.md: check
  every session, give work to every open PR and unblocked issue, close issues whose work is merged, report PRs that
  are ready for auto-merge, tell the owner in 3 lines or fewer. One reminder only (owner: "one reminder is enough"). Do not add a
  send_later.

## Owner-merged PRs

- Changes to process or agent rules (`.claude/rules/`, `docs/agents/`, CLAUDE.md): open the PR and leave it for the
  owner. The owner reviews it and enables auto-merge. Do not enable auto-merge myself.

## Timers

- Workers do not schedule check-ins. Delete any check-in a worker makes. PR events and messages wake sessions; idle
  timers cost money. The hourly lead check-in above is the owner's timer and is the only one.

## Pull request flow

Every pull request follows these steps. The maintainer owns the merge. The lead and the workers never merge.

1. The author branches from the current GitHub `main` (`git fetch origin main`) and opens the pull request ready for
   review, not as a draft. CI runs on it.
2. Before each push, the author runs the local gates:
   - `golangci-lint run` on the touched packages, then `golangci-lint run ./...`.
   - `go test -race` of every touched package. When a store changes, run both stores (SQLite and PostgreSQL).
   - `sqlc diff` when `queries.sql` changes.
   - `make clients-check` when `openapi.json` changes.
   - `make docs-lint` when docs change.

   CI runs the rest: nilaway, semgrep, vuln, trivy, trufflehog, licenses, the full race suite and the e2e jobs.
3. A review gives CLEAN or CHANGES. The author fixes CHANGES in new commits. Nobody amends, rebases or force-pushes.
   To update a branch from `main`, merge `main` into the branch.
4. After a CLEAN review and green pull request CI, the maintainer enables auto-merge (method REBASE). The merge queue
   tests the pull request on top of `main` and merges it. A failed queue run removes the pull request from the queue.
   The author fixes it with new commits, and the pull request is queued again.
5. A stacked pull request targets the branch of the previous pull request. After the previous pull request merges, move
   the base of the stacked pull request to `main`.

- Check every pull request body for closing keywords before the maintainer enables auto-merge.
- The lead does not run tests, conflict fixes or code fixes (owner, 2026-10-16: "you know the rule"). Reading a diff
  and reporting to the maintainer are the lead's work. Everything that runs the build or changes code goes to a worker.

## Nightly architecture review

- Owner, 2026-10-16: scheduled task trig_01H6zGo91E9wkzxw56RnmhfF stays disabled. The owner runs the review when they
  want it. Do not enable it again.
