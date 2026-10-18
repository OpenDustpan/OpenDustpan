# 231. Translation status, the machine-translation note, credits and the review order

Status: accepted · 2026-10-18. Supersedes in part ADR 181 decisions 18 and 19. Extends ADR 181 decisions 12, 20 and 21

## Context

ADR 181 makes the first translation of each language a machine
translation, and needs a native speaker to sign off the term list before a
language ships. On 18 October 2026 the project owner decided the process,
from an idea of the owner's partner: machine translation for now, a check
by native speakers where the owner can find them, and an open request to
the community where a language has no check. This ADR records these
decisions of the owner with the label "(owner)", and the design decisions
that follow from them. The project manager decided the design decisions
under the owner's decision rule. Each such decision has a "Benchmark:" line.

Facts:

- ADR 181 decision 20 marks high-stakes messages in each message file.
  Decision 21 shows a high-stakes message in English until a native speaker
  reviews it.
- The project owner is a native reviewer of English, Dutch and Afrikaans.
- ADR 74 forbids telemetry. The console sends nothing about a person to the
  project on its own.
- ADR 214 makes a user-facing feature ship with its docs page.

## Decision

### Status

1. **Three statuses (owner).** Each translated message and each language
   has one status:

   | Status | Value | Meaning |
   | --- | --- | --- |
   | Machine translated | `machine-translated` | A machine made the text. No person checked it. |
   | Community verified | `community-verified` | A person who reads the language checked the text and signed the check with a name or a handle. |
   | Native reviewed | `native-reviewed` | A reviewer whose first language is that language, and who works in tech, reviewed the text. |

   `en` is the source and has no status. A person can change a message at
   any status. A change by a person with no review sets
   `community-verified` at most.
2. **A message carries its status.** Each message in a message file of a
   language other than `en` has its text, its status, the SHA-256 of its
   English source as 16 hex characters, and the handle of its last
   reviewer, if any:

   ```json
   {
     "stack.delete.confirm": {
       "message": "Stack {stackName} löschen",
       "status": "native-reviewed",
       "source": "4f1c2a9b0d7e3381",
       "reviewer": "example-handle"
     }
   }
   ```

   The build of the console removes the status, the hash and the reviewer.
   The console gets the status of each language from a generated table,
   not from each message. When the English source changes, its hash
   changes. The console check then marks the translation stale. A stale
   translation counts as `machine-translated` until a person checks it
   again.

   Benchmark: GNU gettext marks a translation `fuzzy` when its source
   changes. Mozilla Pontoon keeps a state for each translation:
   unreviewed, approved or fuzzy.
3. **The status of a language.** A language has the highest status that
   at least 95% of its messages hold, and every high-stakes message holds.
   One file of the console lists each language with its status. The
   console check fails when the file claims a status that the messages do
   not hold. The check computes the status. A person does not set it.

### The note and the request to the community

4. **A note on an unchecked language (owner).** When a language has the
   status `machine-translated`, the console and the docs show a note in
   that language and in English: "This text was machine translated. Help
   us fix and check it." The console shows the note in three places: once
   at the top of the page after a person selects the language, until the
   person closes it, in the language switcher next to the language, and in
   the language settings. The docs show it at the top of each translated
   page.
5. **A link to report or fix a string (owner).** Next to the note, a link
   opens a form to report a wrong string. The form is a GitHub issue form
   of the console repository. The link fills in the language, the path of
   the page and the version of the console in the URL. The person adds the
   wrong text and a better text. A second link opens the message file of
   the language on GitHub, for a person who wants to send a fix as a pull
   request. The console sends nothing until the person opens the link and
   sends the form (ADR 74).
6. **Operators can hide the links.** The installation setting
   `console.translationFeedbackLinks` (`true` or `false`, default `true`)
   shows or hides the links of decision 5. The note stays. An installation
   with no access to the internet can hide links that cannot open.
7. **Credit for each translator and reviewer (owner).** The console and the
   docs have a credits page. It lists, for each language, the people who
   translated or checked it, with their role: translator, community
   reviewer or native reviewer. A person is on the page only with their
   consent, under the name or the handle that they give. The page shows no
   email address. One file in the console repository holds the list. The
   pull request of a translation or a review adds its person to it.

   Benchmark: Mozilla Pontoon shows the contributors of each locale on the
   team page of that locale. GitLab lists the proofreaders of each language
   in its docs.

### Review

8. **Machine translation first (owner).** A new string gets a machine
   translation in each language in the pull request that adds it. Its
   inputs are those of ADR 181 decision 19: the English message, its
   description, `GLOSSARY.md` and the term list of the language. The
   machine and its settings are an open question of RFC 78.
9. **Native speakers where the owner finds them (owner).** The owner asks
   native speakers who work in tech to review a language. A review signs
   each message that it checks, as in decision 2.
10. **The project owner reviews English, Dutch and Afrikaans (owner).** The
    project owner is the native reviewer of `en`, `nl` and `af`. When the
    owner checks a message, its status is `native-reviewed`, and its
    reviewer is `project-owner`. The repository never holds the owner's
    personal name for this role. The English source has no status of its
    own (decision 1). Its row in the language status file shows
    `native-reviewed` after the owner checks it, so that English never
    shows the note. The credits page names "the project owner" for these
    three languages.
11. **The review order (owner).** Reviewers check high-stakes messages
    first. ADR 181 decision 20 lists confirmations of destructive actions,
    approvals, security text and billing text. This decision adds the text
    of secrets and every error message. Then come the screens that people
    use most: sign-in, the unit list, a unit, a plan and a job.
12. **A high-stakes message shows in English until a review.** ADR 181
    decision 21 stays. A high-stakes message that is not `native-reviewed`
    shows its English text, with `lang="en"`. This applies at every status
    of the language.
13. **A term list can ship as a draft (owner).** ADR 181 decision 18 needs
    a signed-off term list before a language ships. A language now ships
    with a draft term list that the machine translation step makes from the
    shared list of ADR 232. A native reviewer signs off the list. A language
    cannot have a status above `machine-translated` without a signed-off
    list.

### Releases

14. **New strings never block a release.** A release ships with the
    translations that exist. A new message with no translation shows in
    English, with `lang="en"` (ADR 181 decision 12). A new message with
    only a machine translation shows that translation. If such messages
    take a language below its status (decision 3), the language gets the
    note of decision 4 again. The console check reports each missing,
    stale or unreviewed message. It does not fail the build for them.

    Benchmark: Mozilla Firefox shows the English (`en-US`) text of a string
    that a locale does not have. GitLab shows the English text of a string
    that has no translation in a language.

## Consequences

- ADR 181 decision 18 now applies to a status, not to a release. ADR 181
  decision 19 gets the inputs of decision 8 and the open question of the
  machine.
- `GLOSSARY.md` adds "Translation status" and "Machine-translation note".
- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - The message file format of decision 2, and a build step that removes
    the status fields.
  - The language status file and its check (decision 3).
  - The note, the links, the credits page and the GitHub issue form.
  - The setting `console.translationFeedbackLinks` reaches the console in
    the public sign-in answer (ADR 212 decision 6), next to the preset.
- The server gets the setting `console.translationFeedbackLinks` in the
  settings resource of ADR 212 decision 7. Server text follows the same
  statuses (ADR 233).
- The docs follow decisions 4, 5 and 7 when they have a translation. RFC 78
  asks whether the docs get translations before 1.0.
