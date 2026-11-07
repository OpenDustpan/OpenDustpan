#!/usr/bin/env python3
"""Find owner decisions that do not quote the owner and give a source.

A line that claims an owner decision must show what the owner wrote and
where it came from. The claim is a line with "(owner", "the owner said" or
"owner decision", in any case. The same paragraph or bullet must then hold
both:

- a quoted text in double quotes, and
- a source: a date in the form YYYY-MM-DD, or a link.

The check is a ratchet. The file BASELINE lists the claims that already lack
a quote or a source, one per line, as the file path and the text of the
claim line. The check fails on a claim that is not in BASELINE, and it fails
on a BASELINE entry that no claim matches any more. So the list only shrinks:
a fix removes its entry in the same change.

Usage:
    docs-owner-decisions.py FILE...         check the files against BASELINE
    docs-owner-decisions.py --write-baseline FILE...
                                            rewrite BASELINE from the files;
                                            review the diff, it is the decision
    docs-owner-decisions.py --selftest

The check skips each ADR that is numbered below FIRST_CHECKED_ADR, as the
other docs-lint checks do. The check skips fenced code blocks.
Standard library only.
"""

import collections
import os
import re
import sys

# The first ADR file number that the check reads. Older ADRs are exempt.
FIRST_CHECKED_ADR = 14

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# The known claims that lack a quote or a source. One "path<TAB>claim" per line.
BASELINE = os.path.join(ROOT, "scripts", "docs-owner-decisions.baseline")

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
    """Return True for a file in the ADR directory that is not an ADR from FIRST_CHECKED_ADR on.

    The index of the ADR directory is generated, not an ADR, so it is skipped too.
    """
    if os.path.basename(os.path.dirname(os.path.abspath(path))) != "adr":
        return False
    prefix = os.path.basename(path)[:4]
    return not (prefix.isdigit() and int(prefix) >= FIRST_CHECKED_ADR)


def normalize(line):
    """Collapse the white space of a claim line, so that its key ignores spacing."""
    return " ".join(line.split())


def check_text(text):
    """Return a list of (line, column, reason, claim) for one document.

    The reason is "no quote", "no source" or "no quote and no source". The
    column is the column of the claim in its line. The claim is the normalized
    text of the line that holds it.
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
                    findings.append((number, match.start() + 1, reason, normalize(line)))
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


def repo_path(path):
    """Return the path relative to the repository root, with "/" as separator."""
    return os.path.relpath(os.path.abspath(path), ROOT).replace(os.sep, "/")


def collect(paths):
    """Return a Counter of (repo path, claim) for the claims in the files, skipping pre-checked ADRs."""
    hits = collections.Counter()
    where = {}
    for path in paths:
        if skipped_adr(path):
            continue
        with open(path, encoding="utf-8") as handle:
            for line, column, reason, claim in check_text(handle.read()):
                key = (repo_path(path), claim)
                hits[key] += 1
                where.setdefault(key, []).append((line, column, reason))
    return hits, where


def read_baseline(text):
    """Return a Counter of (path, claim) from the baseline text."""
    entries = collections.Counter()
    for raw in text.splitlines():
        if not raw.strip():
            continue
        path, _, claim = raw.partition("\t")
        entries[(path, claim)] += 1
    return entries


def format_baseline(hits):
    """Return the baseline text for a Counter of (path, claim), sorted."""
    lines = []
    for (path, claim), count in sorted(hits.items()):
        lines.extend([f"{path}\t{claim}"] * count)
    return "\n".join(lines) + ("\n" if lines else "")


def ratchet(hits, baseline):
    """Return (new, stale): the claims over the baseline, and the baseline entries with no claim."""
    new = hits - baseline
    stale = baseline - hits
    return new, stale


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
            [(1, 1, "no quote", "The owner said to use one store on 2026-03-02.")],
        ),
        (
            "fails with no source",
            'The owner said "use one store".',
            [(1, 1, "no source", 'The owner said "use one store".')],
        ),
        (
            "fails with neither a quote nor a source",
            "The owner decision (owner) keeps one store.",
            [(1, 5, "no quote and no source", "The owner decision (owner) keeps one store.")],
        ),
        (
            "a quote in another bullet does not count",
            '- The owner said "one store".\n- Dated 2026-03-02 for another item.',
            [(1, 3, "no source", '- The owner said "one store".')],
        ),
        (
            "a claim with no owner words is not a claim",
            "The team chose one store.",
            [],
        ),
    ]
    for name, text, want in cases:
        got = check_text(text)
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
    if not skipped_adr(os.path.join("docs", "adr", "INDEX.md")):
        failed += 1
        print("selftest: the generated ADR index was not skipped", file=sys.stderr)

    # Ratchet: a claim that the baseline does not list is new, and fails.
    claim = "The owner said to use one store on 2026-03-02."
    base = read_baseline(f"docs/rfc/0001-x.md\t{claim}\n")
    hits = collections.Counter({("docs/rfc/0001-x.md", claim): 1, ("docs/rfc/0002-y.md", claim): 1})
    new, stale = ratchet(hits, base)
    if new != collections.Counter({("docs/rfc/0002-y.md", claim): 1}) or stale:
        failed += 1
        print(f"selftest: new-hit case: new {new}, stale {stale}", file=sys.stderr)

    # Ratchet: a baseline entry with no claim is stale, and fails.
    base = read_baseline(f"docs/rfc/0001-x.md\t{claim}\n")
    new, stale = ratchet(collections.Counter(), base)
    if new or stale != collections.Counter({("docs/rfc/0001-x.md", claim): 1}):
        failed += 1
        print(f"selftest: stale-baseline case: new {new}, stale {stale}", file=sys.stderr)

    # Ratchet: a matching baseline passes, and the baseline round-trips.
    hits = collections.Counter({("docs/rfc/0001-x.md", claim): 1})
    new, stale = ratchet(hits, read_baseline(format_baseline(hits)))
    if new or stale:
        failed += 1
        print("selftest: a matching baseline did not pass", file=sys.stderr)

    total = len(cases) + 4 + 3
    if failed:
        print(f"selftest: {failed} of {total} cases failed", file=sys.stderr)
        return 1
    print(f"selftest: {total} cases passed")
    return 0


def main(argv):
    if argv == ["--selftest"]:
        return selftest()
    if argv[:1] == ["--write-baseline"]:
        paths = argv[1:]
        if not paths:
            print("usage: docs-owner-decisions.py --write-baseline FILE...", file=sys.stderr)
            return 2
        hits, _ = collect(paths)
        with open(BASELINE, "w", encoding="utf-8") as handle:
            handle.write(format_baseline(hits))
        print(f"wrote {sum(hits.values())} entries to {repo_path(BASELINE)}")
        return 0
    if not argv:
        print("usage: docs-owner-decisions.py FILE... | --write-baseline FILE... | --selftest", file=sys.stderr)
        return 2
    hits, where = collect(argv)
    with open(BASELINE, encoding="utf-8") as handle:
        baseline = read_baseline(handle.read())
    new, stale = ratchet(hits, baseline)
    for key in sorted(new):
        for line, column, reason in where[key]:
            print(f"{key[0]}:{line}:{column} owner decision claim not in the baseline: {reason}. Quote the owner and give a date or a link in the same paragraph.")
    for path, claim in sorted(stale):
        print(f"stale baseline entry, no claim matches: {path}: {claim}. Remove its line from {repo_path(BASELINE)}.")
    if new or stale:
        print(f"{sum(new.values())} new claims, {sum(stale.values())} stale baseline entries. Fix the claims, or remove the entries.")
        return 1
    print(f"owner decision claims: {sum(hits.values())}, all in the baseline")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
