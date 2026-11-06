# OpenDustpan lead: owner's standing rules (do not drop on summary)

These are the project owner's own instructions. They hold until the owner changes them.

## Who does the work

- Start one Claude Code session for each task: an author, a reviewer, a merge session or a fix-main session. Select
  the model from the task type, as the routing table in `docs/agents/worker-brief.md` gives: Haiku for mechanical
  work, Sonnet for a bug with a known cause or a normal feature, Opus for design, security and merges.
- Brief each session with `docs/agents/worker-brief.md` on main. A brief adds only the task. A merge session follows
  `docs/agents/merge-session.md`.
- Every session runs `make agent-preflight` first, and `make checks-agent` before it asks for a review.
- The environment config sets GOTOOLCHAIN=go1.27.3 (owner, 2026-10-16), so new sessions need no prompt line for it. If
  make nilaway or make vuln fails on a toolchain mismatch, tell the worker to `export GOTOOLCHAIN=go1.27.3`.
- **Kill (archive) each session when its work item is finished.**
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
- CI runners are the owner's bare-metal machines without sudo. CI changes must not use sudo. The owner installs system
  packages on them (postgresql-client 18 installed 2026-10-16); ask the owner for any new package.

- Keep the pipeline full (owner, 2026-10-16). On every wake: list open ready-for-agent issues and open PRs in BOTH
  repositories, OpenDustpan/OpenDustpan and OpenDustpan/console (owner, 2026-10-17); every unblocked issue and every
  open PR without a live worker gets a Haiku worker now. Do not wait to be asked.

- Swarm watchdog (owner, 2026-10-16): an hourly scheduled task starts a fresh Haiku session that reports to me. When its
  report arrives, act on it, then archive that watchdog session at once.

- mergiraf is installed in all new sessions (owner, 2026-10-16). It resolves many list and import conflicts on merge,
  but its result can be wrong: it once kept a migration twice in schema.go. A worker still runs make generate, make
  check and make e2e after every merge. Workers started before this date may not have it.

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
  every session, give work to every open PR and unblocked issue, close issues whose work is merged, merge PRs that meet
  the merge rule, tell the owner in 3 lines or fewer. One reminder only (owner: "one reminder is enough"). Do not add a
  send_later.

## Owner-merged PRs

- Changes to process or agent rules (`.claude/rules/`, `docs/agents/`, CLAUDE.md): open the PR and leave it for the
  owner. The owner reviews and merges it. Do not merge it myself.

## Timers

- Workers do not schedule check-ins. Delete any check-in a worker makes. PR events and messages wake sessions; idle
  timers cost money. The hourly lead check-in above is the owner's timer and is the only one.

## Merging

- Why: CI is often a bottleneck because of the volume of code we generate while we bootstrap the project.
- A merge session merges a batch of at most 6 pull requests with a CLEAN review. It follows
  `docs/agents/merge-session.md`: it builds the combined tip of the batch on current main, runs the full
  `make checks-agent` once on that tip, drops the last pull request of a red tip, and squash-merges in the batch order.
  A package subset is never a merge gate. Why: pull requests that each passed alone broke main together, and merge
  gates that ran only some packages let 4 breaks reach main from 2026-10-27 to 2026-10-30.
- Authors merge main only for a real conflict or when the PM asks. After a CLEAN review, an author does not change the
  branch. Only the merge session renumbers API versions and migrations.
- At most 2 open pull requests change the same store port or the same table set. The PM keeps this limit.
- Reviews follow `docs/agents/review-protocol.md`. A reviewer starts only after the author posts a `make checks-agent`
  summary with exit 0 on the current head sha. Each reviewer brief holds the session ID of the author. The reviewer
  sends must-fix items straight to the author. I hear only CLEAN, the end of round 3, or a blocker.
- Merge without waiting for CI when the merge session reports the full `make checks-agent` on the combined tip, and
  the review of each pull request in the batch is done.
- Main health (owner, approved 2026-10-17): after each server merge, one worker runs make check (with PostgreSQL) and
  make e2e on the new main head and reports. Do not merge the next server PR until that report is green, or until the
  next PR's worker has run the checks on a head that already contains that main. A red main gets a fix worker at once.
  Why: PRs that each passed alone broke main together about 10 times from 2026-10-11 to 2026-10-17. The gate of the
  next batch runs on a tip that already contains that main, so it meets this rule.
- I do not run tests, merges, conflict fixes or code fixes myself (owner, 2026-10-16: "you know the rule"). Running
  ovcheck, reading a diff, and the merge button are mine; everything that runs the build or changes code goes to a
  worker.
- Still check every PR body for closing keywords before the merge.

## Nightly architecture review

- Owner, 2026-10-16: scheduled task trig_01H6zGo91E9wkzxw56RnmhfF stays disabled. The owner runs the review when they
  want it. Do not enable it again.
