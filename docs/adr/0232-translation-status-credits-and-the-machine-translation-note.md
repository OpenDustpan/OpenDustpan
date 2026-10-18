# 232. Translation status, the machine-translation note, credits and the review order

Status: accepted · 2026-10-18. Supersedes in part ADR 181 decisions 18 and 19. Extends ADR 181 decisions 12, 20 and 21. Supersedes in part ADR 212 decision 6

## Context

ADR 181 makes the first translation of each language a machine
translation, and needs a native speaker to sign off the term list before a
language ships. On 18 October 2026 the project owner decided the process:
machine translation for now, a check by native speakers where the owner
can find them, and an open request to the community where a language has
no check. The owner also decided the translation agent, reports of one
string and the translation of the user documentation, which RFC 78 asked.
This ADR records these decisions of the owner with the label "(owner)",
and the design decisions that follow from them. The project manager decided the design decisions
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
   not from each message. The hash covers the English message text as the
   `en` message file stores it. When the English source changes, its hash
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
   us fix and check it. The credits page names each person who helps." The
   console shows the note in three places: once
   at the top of the page after a person selects the language, until the
   person closes it, in the language switcher next to the language, and in
   the language settings. The docs show it at the top of each translated
   page.
5. **A report of one string (owner).** A person reports one wrong
   string, not a page. Next to the note, the control "Report a translation"
   turns on a report mode. In this mode, each translated string on the
   page gets a report link. The link opens a GitHub issue form of the
   console repository. The person adds the wrong text and a better text. A
   second link opens the message file of the language on GitHub, for a
   person who wants to send a fix as a pull request.

   The URL of a report link holds these values and no others:

   | Value | Example |
   | --- | --- |
   | The language | `de` |
   | The key of the message | `stack.delete.confirm` |
   | The route pattern, never its values | `/namespaces/{namespace}/stacks/{stack}` |
   | The version of the console | `0.9.0` |

   The URL never holds a name, an ID or text of the page. Each link has
   `rel="noopener noreferrer"` and `referrerpolicy="no-referrer"`, so that
   GitHub gets no address of the installation. The form asks the person to
   remove the names and values of the installation from the text that
   they paste. The console sends nothing until the person opens the link
   and sends the form (ADR 74).

   Benchmark: Crowdin In-Context lets a person select one string on the
   page to translate or report it.
6. **Operators can hide the links.** The installation setting
   `console.translationFeedbackLinks` (`true` or `false`, default `true`)
   shows or hides the links of decision 5. The note stays. An installation
   with no access to the internet can hide links that cannot open.
7. **Credit for each translator and reviewer (owner).** The console and the
   docs have a credits page. It lists, for each language, the people who
   translated or checked it, with their role: translator, community
   reviewer or native reviewer. A person is on the page only with their
   consent, under the name or the handle that they give or accept in their
   own pull request. The page shows no email address. One file in the
   console repository holds the list. The pull request of a translation or
   a review adds its person to it. The same rule applies to the `reviewer`
   of a message (decision 2). When a person asks for removal, their name
   or handle becomes `anonymous` in the list and in each message. The
   earlier text stays in the Git history.

   Benchmark: Mozilla Pontoon shows the contributors of each locale on the
   team page of that locale. GitLab lists the proofreaders of each language
   in its docs.

### Review

8. **Machine translation first (owner).** A new string gets a machine
   translation in each language after the pull request that adds it
   merges, or in that pull request when a maintainer runs the step there.
   A missing translation never blocks the pull request (decision 15). Its
   inputs are those of ADR 181 decision 19: the English message, its
   description, `GLOSSARY.md` and the term list of the language.
9. **A translation agent and a review agent (owner).** An AI translation
   agent makes the machine translation. It has the full context of the
   design and the use of the console: the screen that shows the message,
   the message's description, the term lists and `GLOSSARY.md`. It has
   access to the internet, for example to look up the words of a tool's
   own translated docs. The agent first translates a message into Afrikaans.
   It translates the other languages only after the owner proof-reads the
   English source and the Afrikaans text. A second
   agent reviews each translation against the checks of ADR 233 and the
   context. The two agents loop until the review finds no problem.

   - A translation that fails the checks is never committed. The message
     stays missing, the console shows its English text, and the console
     check reports it (decision 15).
   - The repository pins the prompt of each agent, so that a person can
     run the same step again. The repository names no AI model.
   - A maintainer runs the step. CI holds no API key of a model provider.
     The strings are public in the repository, so the provider sees no
     secret.
10. **Native speakers where the owner finds them (owner).** The owner asks
    native speakers who work in tech to review a language. A review signs
    each message that it checks, as in decision 2.
11. **The project owner reviews English, Dutch and Afrikaans (owner).** The
    project owner is the native reviewer of `en`, `nl` and `af`. When the
    owner checks a message, its status is `native-reviewed`, and its
    reviewer is `project-owner`. The repository never holds the owner's
    personal name for this role. The English source has no status of its
    own (decision 1). The language status file has no row for `en`, and English never
    shows the note. The credits page names "the project owner" for these
    three languages.
12. **The review order (owner).** Reviewers check high-stakes messages
    first. ADR 181 decision 20 lists confirmations of destructive actions,
    approvals, security text and billing text. This decision adds the text
    of secrets and every error message. Then come the screens that people
    use most: sign-in, the unit list, a unit, a plan and a job.
13. **A high-stakes message shows in English until a review.** ADR 181
    decision 21 stays. A high-stakes message that is not `native-reviewed`
    shows its English text, with `lang="en"`. This applies at every status
    of the language.
14. **A term list can ship as a draft (owner).** ADR 181 decision 18 needs
    a signed-off term list before a language ships. A language now ships
    with a draft term list that the machine translation step makes from the
    shared list of ADR 233. A native reviewer signs off the list. A language
    cannot have a status above `machine-translated` without a signed-off
    list.

### Releases

15. **New strings never block a release.** A release ships with the
    translations that exist. A new message with no translation shows in
    English, with `lang="en"` (ADR 181 decision 12). A new message with
    only a machine translation shows that translation. If such messages
    take a language below its status (decision 3), the language gets the
    note of decision 4 again. The console check reports each missing,
    stale or unreviewed message. It does not fail the build for them.

    Benchmark: Mozilla Firefox shows the English (`en-US`) text of a string
    that a locale does not have. GitLab shows the English text of a string
    that has no translation in a language.

### The docs

16. **The user documentation gets translations now (owner).** The user
    documentation is `README.md`, `docs/guides/`, `docs/reference/` and
    `docs/explanation/` (ADR 66). Each page gets a machine translation in
    each language of the console, with the process of decisions 8 and 9.
    The statuses, the note, the report links and the credits of this ADR
    apply to each translated page. ADRs, RFCs, `GLOSSARY.md`, `CLAUDE.md`
    and `CONTRIBUTING.md` stay in English only.
17. **A translated page carries its status.** Each translated page records
    its status, the SHA-256 of its English source page as 16 hex
    characters, and its reviewer, as a message does (decision 2). When the
    English page changes, the translation is stale. A stale page shows the
    note and a link to the English page. Code, commands, flags, keys and
    tool output are never translated. The term check of ADR 233 decision 2
    runs on each page.
18. **The docs never block a change.** A feature ships with its English
    page (ADR 214). The translations follow. A missing or stale translation
    is a report, not a failure. The check of internal references (ADR 66)
    runs on each translation. The checks of ASD-STE100 run on the English
    pages only.

    Benchmark: the Kubernetes docs and MDN Web Docs publish translations
    that follow the English pages, and show the English page when a
    translation is missing or old.

## Consequences

- ADR 181 decision 18 now applies to a status, not to a release. ADR 181
  decision 19 gets the inputs of decision 8 and the agents of decision 9.
- ADR 212 decision 6 says that the public sign-in answer holds the preset
  only. This ADR adds `translationFeedbackLinks` to it. ADR 235 adds the
  branding fields.
- `GLOSSARY.md` adds "Translation status" and "Machine-translation note".
- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - The message file format of decision 2, and a build step that removes
    the status fields.
  - The language status file and its check (decision 3).
  - The note, the report mode and its links, the credits page and the
    GitHub issue form.
  - The pinned prompts of the translation agent and the review agent.
  - The setting `console.translationFeedbackLinks` reaches the console in
    the public sign-in answer (ADR 212 decision 6) as
    `translationFeedbackLinks`, next to the preset.
- The server gets the setting `console.translationFeedbackLinks` in the
  settings resource of ADR 212 decision 7. Server text follows the same
  statuses (ADR 234).
- This repository gets the translated user documentation of decisions 16
  to 18, with a status check and a report of missing and stale pages. The
  site generator is not part of this ADR.
