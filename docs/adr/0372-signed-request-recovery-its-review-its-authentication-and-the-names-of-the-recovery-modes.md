# 372. Signed-request recovery: its review, its authentication and the names of the recovery modes

Status: accepted · 2026-11-14. Decided by the project owner, who accepted RFC 130 as written. The owner asked for the new names of decision 1, and the product manager chose them. Decision 6 is a decision of the product manager

Changes:

- Extends ADR 57 decisions 1 and 5: the names of modes 1 and 2. The review use records the recovery mode and the source.
- Changes ADR 46 decision 6: on the route of signed-request recovery only, the signed request stands in place of the factor.
- Changes ADR 142 decisions 1, 2 and 3: a third kind of use, `temp-admin`. The review of a signed-request recovery opens whatever the opt-in.
- Changes ADR 245 decision 3: the review store is a condition of signed-request recovery. The two recovery steps need no fresh authentication.
- Changes ADR 285 decision 17: a temporary administrator of a recovery resets the factors of a person with no fresh authentication.
- Extends ADR 295 decisions 8 and 9: the route also needs the store of break-glass reviews, and its review opens whatever the opt-in.
- Changes ADR 301 decision 14: a temporary administrator of a recovery issues an enrollment code with no fresh authentication.

## Context

ADR 57 gives three break-glass recovery modes. ADR 245 decision 6 gives the
setting `--recovery-mode` the values `bootstrap` (mode 1), `signed-request`
(mode 2) and `none` (mode 3). ADR 295 (owner) lets mode 2 run over the
network: a route accepts a request that an offline operator key signs, and
makes a temporary administrator.

The OpenSpec change of mode 2 was not spec-ready. It conflicted with two
ADRs:

- ADR 142 decision 1 names two kinds of break-glass use, `freeze-break` and
  `account-session`. ADR 245 decision 3 added `temp-admin`, but ADR 142 had
  no link to it. ADR 142 decision 2 does not say what a temporary
  administrator gets when the opt-in `breakGlassReview.enabled` is off.
- ADR 46 decision 6: "What MFA covers. Console sign-in, the creation of
  access tokens, and the reveal of a secret value (ADR 41)." Mode 2 exists
  for the case where the last administrator lost every factor. No factor
  exists to ask for when the route issues its token.

[RFC 130](../rfc/0130-break-glass-mode-2-review-and-authentication.md) gave
four decision points, with the precedent of AWS root user recovery, Google
Workspace super administrator recovery, HashiCorp Vault `operator
generate-root`, the kubeadm `super-admin.conf` and Microsoft Entra emergency
access accounts. On 14 November 2026 the project owner accepted RFC 130 as
written. The owner also said about the name "mode 2": "wtf does that even
mean", and asked for a new name.

RFC 130 left one point open: a temporary administrator holds a bearer
token, and an access token never gives a fresh authentication (ADR 227
decision 6). The reset of the factors of a person (ADR 285 decision 17, and
ADR 301 decision 14 for the enrollment code) asks for a fresh
authentication of the administrator. So a temporary administrator cannot
finish the recovery that it exists for. Decision 6 answers this point.

## Decision

1. **The names of the recovery modes (owner, 2026-11-14).** Mode 1 of ADR
   57 is "bootstrap recovery". Mode 2 is "signed-request recovery". The
   names are the values of the setting `--recovery-mode`: `bootstrap` and
   `signed-request`. Mode 3 keeps its description "no break-glass" and the
   value `none`. The words "over the network" stay where they describe the
   route. Text, code and documentation use these names, not "mode 1" or
   "mode 2".
2. **The kind and the scope of the review (owner, 2026-11-14).** Each
   temporary administrator, from the host command or from the route of
   signed-request recovery, gets one break-glass use of the kind
   `temp-admin`. Its scope is `@server`, its reference is the principal ID
   of the temporary administrator, and its `Until` is the end of the
   temporary administrator. The use records the recovery mode (`bootstrap`
   or `signed-request`) and the source of the request (ADR 57 decision 5).
   The review list can filter on the mode. ADR 142 decision 1 has three
   kinds of use: `freeze-break`, `account-session` and `temp-admin`.
3. **The review opens whatever the opt-in (owner, 2026-11-14).** A
   signed-request recovery opens a review also when
   `breakGlassReview.enabled` is off. With the setting off, the review has
   no deadline and no reminders, as a session of a break-glass account. With
   the setting on, it has the deadline and the notices of ADR 142. Each
   signed-request recovery also sends one notification `break-glass.used` to
   the site administrators at the use, whatever the setting. The notice holds
   the time, the source address and the principal of the temporary
   administrator. It holds no token.
4. **No review store, no route (owner, 2026-11-14).** The route of
   signed-request recovery does not exist until `pkg/breakglass` has a store
   adapter in each dialect. This is a condition on shipping, in addition to
   ADR 295 decision 8. The transaction that makes the temporary
   administrator also writes the notification `break-glass.used` (ADR 142
   decision 3). When that write fails, the transaction fails, and no
   temporary administrator exists. The warning of ADR 245 decision 3 for a
   missing review store applies to the host command only.
5. **The signed request stands in place of the factor (owner,
   2026-11-14).** On the route of signed-request recovery only, the
   signature with the offline operator key stands in place of the factor of
   ADR 46 decision 6. The CLI tool that signs offline reads the key only from
   a file that a passphrase encrypts, so the proof is something that the
   operator has and something that the operator knows. The answer is the
   encrypted token of ADR 295 decision 6. The route makes no console
   session (ADR 245 decision 4). The token lives no longer than the
   temporary administrator (ADR 245 decision 10), and the expiry ends all
   access (ADR 245 decision 13). Every other path keeps ADR 46 decision 6.
   The layers stay independent:
   - The handler checks the size, the shape, the time and the mode.
   - The service checks the signature, the audience and the installation ID
     again.
   - The store inserts the nonce with a unique key in the transaction of the
     temporary administrator (ADR 295 decision 3).
   - The server encrypts the answer to the caller.
   - The authorizer checks each later request against the stored grant of
     the temporary administrator.
   - Each refusal writes an audit entry with the real reason (ADR 295
     decision 5).
6. **The two recovery steps need no fresh authentication (product
   manager).** A temporary administrator of a recovery, from the host
   command or from the route of signed-request recovery, can do exactly two
   steps with no fresh authentication:
   - Reset the factors of a person (ADR 46 decision 9, with the checks of
     ADR 285 decision 17 other than the fresh authentication).
   - Issue an enrollment code for a person (ADR 301 decision 14), so that
     the person adds a new factor.

   Each step writes an audit entry that names the temporary administrator,
   and the review of decision 2 covers each step. The reason, the reach and
   the other checks of each step stay. Every other step that needs a fresh
   authentication refuses a temporary administrator with
   `mfa.fresh-step-required`, as ADR 227 decision 6 says. This includes the
   change of a verified email address of ADR 301 decision 15. The token of a
   temporary administrator still gives no fresh authentication (ADR 227
   decision 6): the two steps do not ask for one when the caller is a
   temporary administrator of a recovery.

   Benchmark: the recovery of an AWS root user with a lost MFA device ends
   with a new device for the user, with no second factor of the user. A
   Google Workspace super administrator who recovered access resets the
   factors of other administrators. A Microsoft Entra administrator issues a
   Temporary Access Pass so that a person enrolls a new factor. Each of these
   recovery paths ends at a new factor, and none of them asks for a factor
   that the recovery replaces.

## Not decided here

- The format of the signed request, the maximum lifetime and clock skew of
  a request, the rotation of the operator key and a second live recovery.
  The product manager decides them in the OpenSpec change of signed-request
  recovery. They touch no owner-tagged decision.

## Consequences

- The OpenSpec change of signed-request recovery is no longer blocked by
  ADR 142 and ADR 46. It must also follow ADR 295 where the draft did not:
  - a temporary administrator and an encrypted token, not a console session,
  - one answer for every refusal,
  - a nonce kept until its not-after time plus 1 hour,
  - the rate limit counter in the database.
- `pkg/breakglass` keeps the three kinds of use. The use gets a field for the
  recovery mode and the source.
- A later wording refinement (ADR 365) changes the words "mode 1" and "mode
  2" in the ADRs, the glossary and the guides to the names of decision 1.
- The user documentation of recovery states the risk statement of ADR 295
  decision 2, the always-on notice of decision 3 and the two steps of
  decision 6.
