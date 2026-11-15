# 385. A fresh step is opt-in

Status: accepted · 2026-11-15. Decided by the project owner (owner, 2026-11-15). The exclusions of decision 5 are the reading of the PM

Changes:

- Changes ADR 212 decision 3: a weaker setting and an approval of a `destroy` need a fresh step only when the installation setting `freshAuthEnabled` is on. Decisions 1 and 2 replace the sentence on built-in MFA off and no SSO provider.
- Changes ADR 225 decision 5: "needs a fresh authentication, always" holds only when `freshAuthEnabled` is on.
- Changes ADR 227 decision 3: "No value turns the check off" now means that no window value turns it off. The setting `freshAuthEnabled` turns it off.
- Extends ADR 227 decision 2: the one window applies only when `freshAuthEnabled` is on.
- Extends ADR 56 decision 5: no step when MFA is off. A person of an SSO provider has no step when `freshAuthEnabled` is off.
- Extends ADR 301 decision 12: no step when `freshAuthEnabled` is off, for every sign-in method.

## Context

The project owner decided on 15 November 2026: "the MFA/step up NEEDS TO BE OPT-IN amend ADR 212".

Built-in MFA is already off by default (ADR 46 decision 5). The step is not opt-in as a whole:

- ADR 212 decision 3 gives a step to an installation with an SSO provider, with no setting that turns it off. It gives no step only when built-in MFA is off and no SSO provider exists.
- ADR 225 decision 5 asks for a fresh authentication for each approval of a `destroy`, "always".
- ADR 227 decision 3 says that no value turns the check off.

The result today: an installation with an SSO provider cannot approve a `destroy` through an access token or with a sign-in that has no fresh step. The code refuses each destroy approval (issue 3257).

## Decision

1. **One installation setting, off by default (owner, 2026-11-15).** The setting `freshAuthEnabled` is a boolean of the installation settings. Its effective default is `false`. The settings API shows the effective value and where it comes from (ADR 212 decision 9). A flag, an environment variable or the configuration file can set it, and that needs no step (ADR 212 decision 3, the static layer).
2. **Off means no step (owner, 2026-11-15).** When the setting is off, the server asks a person for no fresh authentication. This covers each step that ADR 227 decision 2 lists:
   - token creation and rotation, and a secret reveal,
   - the changes of ADR 70 decisions 16, 17 and 23, and the switches of ADR 125 decision 3,
   - a confirmation, a break-glass review and a weaker setting,
   - an approval of a `destroy`, an approval in a namespace with `freshAuthForApprovals`, and an approval rule with `freshAuthSeconds`,
   - a signature of a change order and a ChatOps approval.

   The handler, the service and the store each read the same effective value. None of them keeps a rule of its own.
3. **On means the old rules (owner).** The rules of ADR 212 decision 3, ADR 225 decision 5, ADR 227 and ADR 301 apply. A fresh authentication is a built-in MFA step or a factor of ADR 301. It can also be a new authentication at the SSO identity provider. Its age is at most `freshAuthSeconds`. An access token never gives one. The refusal is `403` with `mfa.fresh-step-required`.
4. **Turning it off is a weaker change (PM).** A change of `freshAuthEnabled` from on to off reduces security. It needs a fresh step while the setting is on (ADR 212 decision 3). A change from off to on needs no step. Both write an audit entry with the old and the new value.
5. **What stays, because it is not a policy step (PM).** These keep their own rules and do not depend on the setting:
   - the proof of a break-glass pass (ADR 373 decision 1, ADR 142),
   - the one-time code of the first administrator (ADR 339 decision 6),
   - the change or reset of a factor (ADR 301 decision 8, ADR 285 decision 17),
   - the issue of an enrollment code (ADR 372 decision 14).

   They protect the sign-in system itself. The owner can widen the setting to them with a new decision.
6. **The stored rules stay and show that they are not enforced (PM).** An approval rule with `freshAuthSeconds` keeps its stored value while the setting is off. A namespace with `freshAuthForApprovals` on keeps it too. The settings API and the rule API show `"enforced": false` for each of them. The server writes no silent change to a rule.
7. **Agents (PM).** ADR 354 stays: an agent never has a fresh authentication, and an agent holds no right that a person has. The setting removes the step for a person. It gives an agent no new action.

## Consequences

- With the setting off, an access token with the right grant can make a setting weaker and approve a `destroy`. The grant and the audit entry are then the only checks. This is the owner's choice. The guide says so in one sentence at the setting.
- The code of `pkg/job` (`newDestroyFresh`, `freshAuth`), `internal/api/jobs/approve.go`, `pkg/store/internal/jobstore` (`checkDestroyProof`), `internal/api/server/freshness.go`, `pkg/gate` and the session settings read one effective value. A test for each layer sends a request with no proof with the setting off (passes) and with it on (refused).
- The refusal of the approval route in `internal/api/jobs/approve.go` ("no session MFA proof yet") is not the answer with the setting off. With the setting on, the route reads the kind and the time of the fresh step from the stored sign-in of the caller. The OpenSpec change for issue 3257 uses this ADR.
- The settings declaration gets `freshAuthEnabled` with the mark "reduces security when it goes from on to off" (ADR 212 decision 3).
- ADR 212, ADR 225, ADR 227, ADR 56 and ADR 301 get the reverse items.
