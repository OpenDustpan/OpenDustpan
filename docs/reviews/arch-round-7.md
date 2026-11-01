# Architecture review, round 7

Range: 2e5f727..da75bb7 (GitHub main on 2026-11-01): 101 commits, trains 27 to 31.
ADRs on main in the range: 297 to 310. ADR 311 is in open PR #2227, not on main.

## Findings filed

Major:

- #2233: the session generation has three models (`session.PrincipalStateStore`
  uint64 with cause and audit entry, `principal.SessionGenerationStore` int64
  read-only, and the private bump of the local user store). PR #2230 adds a
  fourth reader.
- #2234: the `state.write` guard is copied into the tofuhttp, tfe and inventory
  adapters. Only tofuhttp reads the result and audits the refusal. A missing
  gate service fails open in the adapters and closed in the runner claimer.

Minor (umbrella #2235): argon2id adapter imported by pkg/backup; ADR 301 fresh
kinds missing from `session.FreshKind`; GLOSSARY "Session" states the ADR 297
target as fact; ADR 298 decision 6 API not served; sessiontest cookie suites
skip everything while consumers build on the generation.

## Checked with no new finding

- Change links of ADRs 297 to 310: each "Changes" item has its reverse
  "Changed by" item (script check).
- Migrations: one new file, 0162_local_users, in both dialects and the shared
  name list. No tag exists, so nothing shipped is edited.
- API description: `info.version` 0.28.0 to 0.30.0. Every first-parent change
  of `openapi.json` raises it; the pin test enforces it.
- Item key sealing of Pulumi events (train 31): `secret.ItemKeyIndex` writes
  carry the fence. `newItemSealer` fails closed when the store is not an
  index. `storetest.RunEventSealing` covers both adapters.
- `pkg/reviewrequest`: design-only port, tool-neutral, every write fenced and
  audited, depguard domain rule present.
- OTLP sink runs the logsink conformance suite. Its destination is operator
  configured (no phone-home).
- sshsig runs `stepuptest.RunVerifier`.
- Collection access review, restore blob checks, S3 probe retry: covered
  by #2211, #2176 (closed), #2195 (closed).
- Already filed, so not refiled: #2208, #2209, #2216 (state.write gates),
  #2186 (adapters in pkg/), #2230 and #2232 (local users).
