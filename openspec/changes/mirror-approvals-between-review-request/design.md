## Context

Change orders hold approvals as signatures. Review requests hold approvals as forge reviews. The two stay in sync for the same person only when the forge connection links identity through OAuth. This change assumes review modes 2 and 4 exist and does not define them. It calls the destroy fresh check owned by policy-approvals for mirrored signatures.

## Decisions

1. **Pending review then fresh-checked signature.** A forge approve or request-changes event creates a pending review for the linked principal and never writes a signature directly. The pending review becomes a signature only when the same linked principal completes the same approval-rule and fresh-authentication checks as a direct approve, with the fresh proof checked at the point of signature write. Each direction writes one audit entry with the source.
2. **Read-only without OAuth.** Without an OAuth link the change-order page for units that come from version control reads but refuses approve, request-changes and dismiss with a 403 reason that names the missing OAuth link. Reads stay open.

## Structure

Unexported type approvalMirror with constructor newApprovalMirror(linkResolver, freshChecker) that builds only linked, fresh-checked signatures; no direct signature write path bypasses it.

## Risks

- A forge identity links to the wrong principal and grants a false approval.
- A replayed forge event writes a duplicate signature.
- A concurrent duplicate delivery writes two signatures.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| intake | Forge webhook intake checks the hook secret and the event shape before any mapping. | Send a forged approve event with a wrong secret directly to the intake; the server refuses it and writes no pending review and no signature. |
| service | Mirror service resolves the forge identity only through the stored OAuth link owned by the same person and rechecks link ownership at the point of signature write for both the forge path and the API path; unlinked, mismatched, revoked or changed links map to no signature. | Send a valid approve event for a forge user with no link, with a link owned by another person, and with a link revoked between event and signature write; the server records no signature in each case. |
| handler | Approve path checks the fresh-authentication window at the point of use for the linked principal, with the shortest of installation, namespace and approval-rule windows, including window 0. | Submit a pending forge approval with a stale fresh proof for a destroy change and for a rule-level freshAuthSeconds change; the server answers 403 mfa.fresh-step-required and writes no signature. |
