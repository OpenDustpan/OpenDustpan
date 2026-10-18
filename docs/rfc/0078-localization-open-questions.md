---
rfc: 78
title: "Localization open questions: translated docs, the translation machine, the server formatter and the edges"
status: discussing
created: 2026-10-18
---

# RFC 78. Localization open questions: translated docs, the translation machine, the server formatter and the edges

Status: discussing.

## Problem

ADRs 231 to 236 decide the languages, the direction, the translation
statuses, the message checks, the server text, the branding and the
accessibility gates. Seven questions stayed open. They had no single
answer that Google, AWS, Mozilla and GitLab share, or they needed a
dependency, which needs the owner's approval. On 18 October 2026 the owner
answered each of them. Two parts stay open: a date form for each locale in
notices (question 3), and the time axis of charts in right-to-left
languages (question 6), which waits for research.

## Questions, options and proposals

### 1. Docs in other languages before 1.0

The user documentation is `README.md`, `docs/guides/`, `docs/reference/`
and `docs/explanation/` (ADR 66). It changes in each pull request of a
feature (ADR 214). The question was whether it gets translations before
1.0.

| Option | Cost | Gain |
| --- | --- | --- |
| A. English only before 1.0 | None. | The docs change fast before 1.0. Each translation would go stale in days. |
| B. Machine translation of all docs in the 33 languages, with the note of ADR 232 | A build step, a translation run for each change, and many stale pages. | Readers who do not read English get every page. |
| C. Machine translation of a small set: the quickstart, the install guide and the pages that the console links | A build step and a run for about 10 pages. | The first steps work in each language. The rest stays English. |
| D. The languages that the owner reviews (Dutch and Afrikaans) and nothing else | Owner time for each change. | Reviewed text, for few readers. |

Proposal: A. This was the recommendation of the project manager.

Decision (owner): B, now, with the same process as the console. ADRs,
RFCs, `GLOSSARY.md`, `CLAUDE.md` and `CONTRIBUTING.md` stay in English. A
translation never blocks a change: a feature ships with its English page,
and a missing or stale translation is a report. ADR 232 decisions 16 to 18
record it. The site generator is not decided.

### 2. The translation machine

ADR 232 decision 8 makes a machine translation for each new string. Its
inputs are the English message, its description, `GLOSSARY.md` and the term
list.

| Option | Cost | Gain |
| --- | --- | --- |
| A. A large language model, with the inputs in its prompt | An API key in CI or on a maintainer's machine. The output varies between runs. | It reads the description and the glossary as context. It covers all 33 languages. |
| B. DeepL with a glossary for each language | An API key. A glossary holds pairs of terms, not descriptions. | Stable output. Not every language of the set may have glossary support. |
| C. Google Cloud Translation (Advanced) with glossaries | An API key and a cloud project. | Glossaries for each language. Stable output. |
| D. People only | No machine. Many languages stay empty for a long time. | No machine errors. |

Proposal: A, run by a maintainer, with the checks of ADR 233 as the gate.
The checks catch the errors that a machine makes: a translated term, a
lost argument, a lost plural category. The strings are public in the
repository, so the provider sees nothing secret.

Decision (owner): A. An AI translation agent with the full context of the
console and access to the internet first translates into Afrikaans. It
translates the other languages only after the owner proof-reads the
English source and the Afrikaans text. A second agent reviews, and the
two loop until the review is clean. A translation that fails the checks is
never committed. The repository pins the prompts and names no model. A
maintainer runs the step, and CI holds no API key. ADR 232 decision 9
records it.

### 3. The Go formatter of server messages

ADR 234 decision 11 needs ICU MessageFormat on the server, with the plural
rules of CLDR. `golang.org/x/text` is an indirect dependency today. Its
package `feature/plural` holds the CLDR plural rules. Its package `message`
uses its own format, not ICU MessageFormat.

| Option | Cost | Gain |
| --- | --- | --- |
| A. A small ICU subset in `internal/`: plain text, arguments, `plural`, `select` and named formats, with `x/text/feature/plural` | About 500 lines and a test suite against the FormatJS results. `x/text` becomes a direct dependency. | One message format in both repositories. No new module. |
| B. A third-party Go library for ICU MessageFormat | A new module. The ones known to the author have few maintainers. | Less code to write. |
| C. `x/text/message` with its own format | Two formats in the project. The checks of ADR 233 run twice. | A maintained module. |
| D. Notices stay in English | No code. | Nothing for the recipient. |

Proposal: A. It needs the owner's approval to make `golang.org/x/text` a
direct dependency.

Decision (owner): A. `golang.org/x/text` is a direct dependency, for
language matching, plural rules and number formatting. Dates and times in
notices use one fixed form in every language, for example
`2026-10-18 15:33 UTC`, in the time zone of the person, with Western digits. ADR
234 decisions 6 and 11 record it.

Still open: a date form for each locale in notices. It needs the date data
of CLDR, which `golang.org/x/text` does not format today.

### 4. The product name in chat messages

The owner's first rule named the console and emails only. Chat messages are not in the rule.

| Option | Effect |
| --- | --- |
| A. Chat messages use the product name, as emails do | One rule for every text that reaches a person. |
| B. Chat messages name no product | The chat app's own name and icon name the sender. This is the state until a decision. |
| C. Chat messages say "OpenDustpan" | The chat shows a name that the operator replaced everywhere else. |

Proposal: A, if the owner agrees that "emails" in the rule stands for
notices to people.

Decision (owner): A. ADR 235 decision 4 records it.

### 5. The language of a person who never set one

ADR 234 decision 5 first used the installation default when `language` is
`null`. The console uses the browser for such a person, so the console and
the emails can differ.

| Option | Effect |
| --- | --- |
| A. The installation default (the state now) | No new field. The emails can be in another language than the console. |
| B. The console writes the matched language to a read-only field `resolvedLanguage` of the preferences after sign-in | The emails follow the console. The server keeps one more field that the person did not set. |
| C. The console asks for a language at the first sign-in | One more step for each new person. |

Proposal: B. Its value is the result of the browser match of ADR 181
decision 2, and the person's own `language` always wins over it.

Decision (owner): B. Emails use the person's `language`, then
`resolvedLanguage`, then the installation default. ADR 234 decision 5
records it.

### 6. The time axis of charts in right-to-left languages

ADR 231 decision 12 keeps time from left to right until a decision.

| Option | Effect |
| --- | --- |
| A. Left to right in every language | Charts read as in most engineering tools. A right-to-left reader reads the axis against the page. |
| B. Right to left in a right-to-left language | The chart follows the page. Screenshots and shared links differ between languages. |

Proposal: ask the native reviewers of Arabic and Hebrew, then decide. Keep
A until then.

Decision (owner): research the precedent first: what other products do,
and what their users find easy to read. Keep A until then. ADR 231
decision 12 records it. The question stays open.

### 7. A report of one string from the page

ADR 232 decision 5 first reported a wrong string through a form of the page,
not the message.

| Option | Effect |
| --- | --- |
| A. The form of the page only (the state now) | The reviewer finds the message from the text. |
| B. A translation mode in settings that shows the ID of each message next to its text | The report names the message. One more mode to build and to test. |

Proposal: A for now. B when the number of reports shows that A is too
slow.

Decision (owner): a report of one string now, not a form of the page. ADR
232 decision 5 records it: a report mode gives each translated string its
own link.

## Outcome

Accepted in part on 2026-10-18. ADR 232 records questions 1, 2 and 7. ADR
234 records questions 3 and 5. ADR 235 records question 4. ADR 231 decision
12 records the interim rule of question 6. The owner also approved
`@axe-core/playwright` and `axe-core` for the console (ADR 236).

Open: a date form for each locale in notices, and the time axis of charts
in right-to-left languages after the research.
