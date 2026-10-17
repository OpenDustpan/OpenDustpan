# Lead check-in

The hourly scheduled task sends the lead session to this file. Do these steps in order. Do not ask the owner first.

1. Read `docs/agents/lead-workflow.md` on main again.
2. Call list_sessions (mine: true, limit 100). The result is saved to a file. Run `python3 docs/agents/lead_sessions.py <that file>`. For each live session: BLOCKED or NEEDS: unblock it now (send_message with priority now). Idle (REVIEW_READY or COMPLETED) for more than 30 minutes: read its last result with list_events (kinds ["result"], limit 3), then give it its next step, or archive it if its item is finished. Keep each title as "<role>: PR N (state)".
3. List open PRs and open ready-for-agent issues in OpenDustpan/OpenDustpan and OpenDustpan/console. Every open PR and every unblocked issue without a live session gets a worker or reviewer now (brief: `docs/agents/worker-brief.md`).
4. Close every open issue whose work is merged on main (state_reason completed, a one-line comment that names the merge commit).
5. Merge every PR that meets the merge rule in `docs/agents/lead-workflow.md`.
6. Tell the owner in 3 lines or fewer what changed. If nothing changed, say "No change" and nothing else.
