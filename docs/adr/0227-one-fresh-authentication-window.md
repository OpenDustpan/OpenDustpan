# 227. One fresh-authentication window

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes ADR 56 decisions 2 and 3, and ADR 71 decisions 1 and 2. Supersedes the age of a confirmation in ADR 103 decision 2 and ADR 128 decision 7. Supersedes the age of ADR 142 decision 9. Supersedes the window and the "set to 0" item of ADR 212 decision 3. Answers an open question of ADR 191 and ADR 192

Changed by:

- ADR 301 extends decision 1: a passkey or TOTP is the fresh authentication
  of a person of a plain OAuth 2.0 provider or a trusted header.
- ADR 320 extends decision 1: the request and the check of a fresh
  authentication for each method.

## Context

Five rules ask for a fresh authentication, with different windows and two
meanings of 0:

- ADR 56 decisions 2 to 4: two windows of 15 minutes for built-in MFA, one
  for token creation and one for a secret reveal. A window of 0 asks for a
  step at each operation.
- ADR 71 decisions 1 and 2: `tokenFreshnessSeconds` for SSO sessions, 15
  minutes by default, 60 seconds to 7 days, and 0 turns the check off.
- ADR 103 decision 2 and ADR 128 decision 7: a confirmation at most the
  lower of 15 minutes and W old.
- ADR 142 decision 9: a break-glass review at most 15 minutes old.
- ADR 212 decision 3: a change that makes a setting weaker, and an approval
  of a `destroy`, use the window of ADR 56 or ADR 71. The list of weaker
  changes holds "set to 0".

No tag shipped a window setting, so no data conversion is needed (ADR 73).

## Decision

1. **A fresh authentication.** A fresh authentication is a built-in MFA step
   (ADR 46) or a new authentication at the SSO identity provider (ADR 71).
   It is fresh when its age is not above the window.
2. **One window everywhere (owner).** The installation has one setting,
   `freshAuthSeconds`, with the default 900 (15 minutes). Each step that an
   ADR names as "fresh MFA", "a fresh MFA step" or "a fresh
   authentication" uses it: token creation and rotation, a secret reveal,
   the changes of ADR 70 decisions 16, 17 and 23, the switches of ADR 125
   decision 3, a confirmation, a break-glass review, a weaker setting, an approval when ADR 225 decision 5 or its approval
   rule asks for one, and a ChatOps approval (ADR 146) on the same terms.
3. **0 asks every time (owner, 2026-10-18).** A window of 0 asks for a
   fresh authentication at each step. No value turns the check off. The
   other values are 60 to 900 seconds. A window of 0 is the strictest
   value, so a change to 0 needs no fresh authentication. A change to a longer window
   needs one, as ADR 212 decision 3 says for a weaker setting.
4. **Narrower is stricter.** A namespace and an approval rule
   (`freshAuthSeconds`, ADR 225) can set a shorter window or 0. They never
   set a longer one. The step uses the shortest window that holds for it.
5. **Signatures.** A signature of a change order needs a fresh
   authentication when its approval rule has `freshAuthSeconds`, when the
   change holds a `destroy`, or when the namespace has
   `freshAuthForApprovals` (ADR 225 decision 5). This answers the open
   questions of ADR 191 and ADR 192.
6. **Errors.** A step with no fresh authentication answers `403` with the
   error code `mfa.fresh-step-required` and changes nothing (ADR 212
   decision 3). The error names the path of a new sign-in. An access token
   never gives a fresh authentication.

Benchmark: the spike stored `freshAuthSeconds` as none, 0 or a window, and
the approver gave the age of the authentication. One field gave each rule of
its tests.

## Consequences

- `tokenFreshnessSeconds` of the session settings becomes
  `freshAuthSeconds`, and 0 changes meaning. The code of `pkg/session`
  changes in the thin slice.
- The two windows of ADR 56 become one.
