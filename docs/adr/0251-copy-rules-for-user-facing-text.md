# 251. Copy rules for user-facing text

Status: accepted · 2026-10-19

## Context

Copy is every text that a user reads: the console, the documents in
`README.md`, `docs/guides/`, `docs/reference/` and `docs/explanation/`, CLI
output, and the text of API errors and messages. Today this text uses our
internal words. A user reads "forge", "unit", "review request", "write
fenced: stale namespace failover version" and "sealed". These words mean
nothing to a user, or they mean something else in the industry. For example,
"sealed" means locked in Vault.

ADR 14 sets the grammar of the text. ADR 66 removes internal references from
it. This ADR sets which words the text uses.

The project owner accepted these decisions on 2026-10-19.

## Decision

1. Name the concrete thing first.
   - Use a plain generic term only when the text must cover every kind of
     the thing. Never use our internal word in copy.
   - Write "GitHub" or "Forgejo". The generic term is "Git host", never
     "forge".
   - Write "stack" or "workspace". The generic term is "stacks and
     workspaces", never "unit".
   - Write "pull request" for GitHub and Forgejo, and "merge request" for
     GitLab. The generic term is "pull request or merge request", never
     "review request".
   - Write a write-back as "commits to <repo> on GitHub".
   - The one short exception is the label "Inventory" in the console
     navbar for the list that covers all tools.
2. Words for internal mechanisms stay in code, API fields and logs. Copy
   says the effect and the one next step. For example, "write fenced: stale
   namespace failover version" becomes "The namespace changed while this
   write ran. Run the command again."
3. Keep an industry word only when its common meaning matches ours. Explain
   it once, where it first shows. For example: "principal: a person, a
   service or a workload". Do not use "sealed" in copy, because it means
   locked in Vault. Write "encrypted by the server".
4. One word has one meaning. A glossary term is never also a plain verb or a
   second concept in copy. "Reach", "scope" and "fence" break this rule
   today.
5. An identifier or an action name comes after plain words, in monospace,
   and never alone. Write "You need permission to manage the tokens of this
   service principal (`principal:manage-tokens`)."
6. `GLOSSARY.md` records the copy word beside each term that a user can meet.
   The line `_Copy says_:` follows the line `_Avoid_:`. The glossary term
   stays the word of code, API fields, wire values, metrics and logs.

## Consequences

- A change to copy follows these rules. A reviewer refuses copy that uses
  an internal word.
- The console follows the same rules. Its `CLAUDE.md` is in another
  repository and needs a matching change.
- ADR 14 and ADR 66 stay in force. This ADR adds to them.
- Existing copy changes over time, as the lines around it change. One issue
  for each area tracks the larger rewrites.
