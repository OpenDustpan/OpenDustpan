# 250. Defense in depth for security code

Status: accepted · 2026-10-19. The project owner decided it.

## Context

A security audit found about 10 low-severity findings. Each one is safe today only because its input is safe, so a later change can turn it into a real fault. One check for each risk is one hole away from failure. In the Swiss cheese model (https://en.wikipedia.org/wiki/Swiss_cheese_model), harm gets through only when the holes in every slice line up. Defense in depth (https://en.wikipedia.org/wiki/Defense_in_depth_(computing)) puts several independent layers in the path.

## Decision

1. **Never only one layer.** Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others.
2. **Never either/or.** Check and encode and limit. A value that you checked is still encoded where it is used. A sandboxed process still gets input that you checked. "It is checked upstream" is never a reason to remove, skip or omit a check.
3. **The layers.** Apply every layer that fits. The lists below are examples. They are not complete.
   - Check at the boundary: an allowlist of type, length, character set and range. Reject bad input. Never change it to make it valid.
   - Encode or contain at the point of use, for that context. Examples: SQL placeholders only. Paths through `os.Root`, or `filepath.Rel` with a containment check after cleaning. `exec.Command` with an argument list, never a shell. `html/template`. Remove CR and LF from header and log values. Check the resolved IP address when an outbound connection opens, not only the host name.
   - Limit the effect: least privilege, scoped and short-lived tokens, size limits (`http.MaxBytesReader`), a timeout on every external call, a sandbox for untrusted work.
   - Authorize at the point of use, for the object, not only the action and not only at the route.
   - Fail closed and record: an unknown value, a parse error, missing configuration or a dependency error means deny. Every denial and every use of a privilege goes to the audit log.
4. **Every layer has its own test.** The test sends hostile input directly to that layer, as if every other layer had failed. A layer without its own test counts as missing.
5. **Everything outside the process is untrusted.** Examples, not a complete list: HTTP requests (path, query, headers, body, cookies). Tool state files and plans. Webhooks. Data from GitHub, GitLab and Forgejo. Runner output. Also configuration files and environment variables, backups that you restore, database rows written by another version, and file names inside archives.
6. **Proven primitives only.** Use the standard library or well-known primitives (`crypto/subtle.ConstantTimeCompare`, `crypto/rand`). No custom cryptography, parsers or escaping.
7. **A check that looks redundant carries a comment.** The comment says that the check is a defense-in-depth layer. It names the other layers that the check does not rely on. This tells reviewers that the choice is on purpose. It also keeps a refactoring sweep from removing the check. Example:

   ```go
   // Defense in depth: the handler validates the name too; this check stays so that a new caller cannot skip it.
   ```

   The rule against internal references in code applies. The comment gives the reason, not an ADR number.
8. **Review rule.** For a security-relevant change, the review lists each layer and its test. A missing layer, a missing layer test or a redundant check without its comment is a blocking finding, never a nit.

## Consequences

Security paths get more code and more tests. Some checks look redundant on purpose, and each one says so in a comment. Reviewers do not ask to remove a commented defense-in-depth check. A change that removes one needs its own security review.
