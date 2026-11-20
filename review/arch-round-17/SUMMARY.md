# Architecture review, round 17

Range: 4a78446d11cb..4daa46bceabe on main (108 commits). Commits ranked by size and risk; top areas read in depth against docs/adr (Current rule), GLOSSARY.md, CLAUDE.md.

## Issues filed
- #3809 restore: keep-access restore takes temp_admins rows from the backup; a row naming a live admin with a past expiry makes the next sweep disable that admin (needs a change to ADR 253).
- #3811 service tokens (#3807): after-change lost-maker check reads the per-request access cache; a maker who removes their own group path keeps the token.
- #3812 service tokens (#3807): Pulumi update lease requests use CheckActive, which does not check the maker.
- #3810 break-glass (#3646): pass consumed before the decision record is stored; record names an unchecked pass ID from the request (latent).
- #3813 minor: damaged-job cancel ETag v0 and empty old state in audit; confirm-rotation 200 after window end; webhook guide promises auto resend; restore setting change unaudited on read failure; durable doc comments contradict code.

## Covered, no defect
First admin code (#3672); bulk lock/unlock (#3665); restore ends running updates (#3645); saved-view restore limits (#3791); webhook intake HMAC/limits/replay (#3676); mTLS exchange checks (#3658); IP allowlist lists; audit page token; durable deadline limit; damaged-job cancel authz; stack no-collection write refusal (#3631).

## Design notes (no issue)
- mTLS exchange route merged but unusable: no listener requests client certs, no revocation source (documented; #3566 open; OpenSpec tasks 3.3-3.5 unchecked).
- Break-glass conformance cases not in an exported gatetest suite.
- Lost-maker change has no e2e step; checks every active token after every access change.
- Restore keep/replace decided at request time, not swap; other security settings still come from the backup.
- Restored running updates stay running; audit gives only a count.
- Webhook receipts not fenced; delivery ID dedup case-sensitive.
- mTLS guard has one global rate window.
- Every-object list fails closed for all admins above 1000 allowlisted namespaces.
- Durable deadline-release limit/count not shown in the API.
- Audit page token bound to namespace name, not stable ID.

## Not covered
Log/stderr escaping (ef6849d); serve TLS/plain-HTTP refusal and flag naming (476e0d8, 7d3551d, dfb9e2a); SQLite file modes (e254f6e); sensitive variable clearing (d88f9e3); LDAP checks (7cb9e56, abb1887, ba0dadc); masked state read bounds (f7f8e50); damaged-row resolvers (dc0613a); stack tags audit (bb78be9); grant ID shape (0310a18); reach walker (c36f490); code rules audit (8503e1b, 59932c5); decision record retention (7a10c26); release/CI/CodeQL/Scorecard/fuzz/image/chart; most of the 51 changed docs pages. No test suite, make check or make e2e was run.
