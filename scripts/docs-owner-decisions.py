#!/usr/bin/env python3
"""Find owner decisions that do not quote the owner and give a source.

A line that claims an owner decision must show what the owner wrote and
where it came from. The claim is a line with "(owner", "the owner said" or
"owner decision", in any case. The same paragraph or bullet must then hold
both:

- a quoted text in double quotes, and
- a source: a date in the form YYYY-MM-DD, or a link.

Usage:
    docs-owner-decisions.py FILE...   report each claim, exit 1 when the count is 30 or fewer
    docs-owner-decisions.py --selftest

When more than 30 claims lack a quote or a source, the check prints them as
warnings and exits 0. The limit is WARN_ABOVE. The check skips each ADR that
is numbered below FIRST_CHECKED_ADR, as the other docs-lint checks do. The
check skips fenced code blocks.
Standard library only.
"""

import os
import re
import sys

# Above this count of findings the check only warns. At or below it, the check fails.
WARN_ABOVE = 30

# The first ADR file number that the check reads. Older ADRs are exempt.
FIRST_CHECKED_ADR = 14

# The owner claim, in any case.
CLAIM_RE = re.compile(r"\(owner|the owner said|owner decision", re.IGNORECASE)

# A quoted text: ASCII double quotes, or typographic double quotes.
QUOTE_RE = re.compile(r'"[^"\n]+"|“[^”\n]+”')

# A source: a date in the form YYYY-MM-DD, or a link (URL or Markdown link).
SOURCE_RE = re.compile(r"\b\d{4}-\d{2}-\d{2}\b|https?://|\]\(")

FENCE_RE = re.compile(r"^\s*(```|~~~)")

# A line that starts a list item, a table row, a heading or a quote starts a
# new block, so it ends the paragraph before it.
BLOCK_START_RE = re.compile(r"^\s*(?:[-*+]\s|\d+[.)]\s|\||#{1,6}\s|>)")


def skipped_adr(path):
    """Return True for an ADR file numbered below FIRST_CHECKED_ADR."""
    if os.path.basename(os.path.dirname(os.path.abspath(path))) != "adr":
        return False
    prefix = os.path.basename(path)[:4]
    return prefix.isdigit() and int(prefix) < FIRST_CHECKED_ADR


def check_text(text):
    """Return a list of (line, column, reason) for one document.

    The reason is "no quote", "no source" or "no quote and no source". The
    column is the column of the claim in its line.
    """
    findings = []
    in_fence = False
    block = []  # (line number, line) of the open paragraph

    def flush():
        if not block:
            return
        joined = "\n".join(line for _, line in block)
        has_quote = QUOTE_RE.search(joined) is not None
        has_source = SOURCE_RE.search(joined) is not None
        if not (has_quote and has_source):
            if not has_quote and not has_source:
                reason = "no quote and no source"
            elif not has_quote:
                reason = "no quote"
            else:
                reason = "no source"
            for number, line in block:
                match = CLAIM_RE.search(line)
                if match:
                    findings.append((number, match.start() + 1, reason))
        block.clear()

    for number, line in enumerate(text.splitlines(), start=1):
        if FENCE_RE.match(line):
            flush()
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        if not line.strip():
            flush()
            continue
        if BLOCK_START_RE.match(line):
            flush()
        block.append((number, line))
    flush()
    findings.sort()
    return findings


def selftest():
    failed = 0
    cases = [
        (
            "passes with a quote and a date",
            'The owner said "use one store" on 2026-03-02, so we keep it.',
            [],
        ),
        (
            "passes with a quote and a link",
            'The owner decision is "one store" ([record](https://example.com/r)).',
            [],
        ),
        (
            "passes with a typographic quote and a date",
            "The owner decision “one store” was made 2026-03-02.",
            [],
        ),
        (
            "fails with no quote",
            "The owner said to use one store on 2026-03-02.",
            [(1, 1, "no quote")],
        ),
        (
            "fails with no source",
            'The owner said "use one store".',
            [(1, 1, "no source")],
        ),
        (
            "fails with neither a quote nor a source",
            "The owner decision (owner) keeps one store.",
            [(1, 5, "no quote and no source")],
        ),
        (
            "a quote in another bullet does not count",
            '- The owner said "one store".\n- Dated 2026-03-02 for another item.',
            [(1, 3, "no source")],
        ),
        (
            "a claim with no owner words is not a claim",
            "The team chose one store.",
            [],
        ),
    ]
    for name, text, want in cases:
        got = [(line, column, reason) for line, column, reason in check_text(text)]
        if got != want:
            failed += 1
            print(f"selftest: {name}: got {got}, want {want}", file=sys.stderr)

    before = f"{FIRST_CHECKED_ADR - 1:04d}"
    first = f"{FIRST_CHECKED_ADR:04d}"
    skip_path = os.path.join("docs", "adr", before + "-old-decision.md")
    keep_path = os.path.join("docs", "adr", first + "-new-decision.md")
    if not skipped_adr(skip_path):
        failed += 1
        print("selftest: the ADR before the first checked one was not skipped", file=sys.stderr)
    if skipped_adr(keep_path):
        failed += 1
        print("selftest: the first checked ADR was skipped", file=sys.stderr)
    if skipped_adr(os.path.join("docs", "rfc", before + "-open-question.md")):
        failed += 1
        print("selftest: an RFC was skipped by the ADR rule", file=sys.stderr)

    total = len(cases) + 3
    if failed:
        print(f"selftest: {failed} of {total} cases failed", file=sys.stderr)
        return 1
    print(f"selftest: {total} cases passed")
    return 0


def main(argv):
    if argv == ["--selftest"]:
        return selftest()
    if not argv:
        print("usage: docs-owner-decisions.py FILE... | --selftest", file=sys.stderr)
        return 2
    findings = []
    for path in argv:
        if skipped_adr(path):
            continue
        with open(path, encoding="utf-8") as handle:
            for line, column, reason in check_text(handle.read()):
                findings.append((path, line, column, reason))
    for path, line, column, reason in findings:
        print(f"{path}:{line}:{column} owner decision claim: {reason}. Quote the owner and give a date or a link in the same paragraph.")
    count = len(findings)
    if count == 0:
        return 0
    if count > WARN_ABOVE:
        print(f"warning: {count} owner decision claims lack a quote or a source. The limit of {WARN_ABOVE} is exceeded, so this check only warns.")
        return 0
    print(f"{count} owner decision claims lack a quote or a source. Fix each one above.")
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
