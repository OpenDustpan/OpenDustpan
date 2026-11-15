## Context

Dusty runs as a chain of durable turn operations. The issue asks for measured data on three hold patterns. This change writes only a benchmark note. It changes no server behavior and adds no wait state, route, or gate.

## Decisions

1. **Benchmark method.** Survey two outside agent products, then simulate each hold pattern against the same approval wait on one dev server build with 3 fixed prompts, 5 repeats per pattern, median and range reported. Record token ledger rows per attempt, peak resident memory in megabytes, and seconds from approval to next model call.
2. **Pattern definitions.** Suspend does no polling work during the wait. Poll rechecks every 60 seconds. End and restart closes the turn and opens a new turn after approval. The harness is an uncommitted local script.
3. **Restart matrix.** Rows: database gap of 10 seconds, gap of 90 seconds, restart during a read, model, check and proposing step, claim takeover by a second instance, and cancel during a wait. Each row records what resumes, what repeats at what token cost, what is lost, and whether a repeated proposing step yields one change.
4. **Scale, limit and credential rows.** Scale rows use 1 and 50 concurrent waiting runs per pattern and record peak memory and poll load. Limit rows set the run time limit to 60 minutes per run and hold the wait to 65 minutes per run. Credential rows record expiry and revocation during a wait. The note names model, provider, cache mode, prompt set, build and database, and says when token counts are harness counts.
5. **Recommendation rule.** Recommend one pattern with the numbers that support it and the failure modes it accepts.

## Structure

No code structure changes. The note is a document under docs/. No bug class is removed.

## Risks

- Numbers from a dev server do not match production memory or timing.
- Token counts vary between runs, so the note reports median and range over 5 repeats.
