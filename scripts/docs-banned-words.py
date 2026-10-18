#!/usr/bin/env python3
"""Find banned words and emoji in the project's prose.

The words claim quality instead of showing it, and readers take them as a
sign of text that nobody checked. The README, the docs and the community
files must name the thing and the number instead.

Usage:
    docs-banned-words.py FILE...   report each finding, exit 1 if any
    docs-banned-words.py --selftest

The check reads Markdown prose only. It skips fenced code blocks and inline
code spans, because commands and wire values keep their own words.
Standard library only.
"""

import os
import re
import sys

# Each entry is a regular expression for one word and its forms, and the
# advice that the report prints.
BANNED = [
    (r"comprehensive(ly)?", "say what it covers"),
    (r"robust(ly|ness)?", "name the failure it survives"),
    (r"seamless(ly)?", "say what the user does not have to do"),
    (r"blazing(ly)?([- ]fast)?", "give the measurement"),
    (r"leverag(e|es|ed|ing)", "use 'use'"),
    (r"pivotal", "say why it matters"),
    (r"showcas(e|es|ed|ing)", "use 'show'"),
    (r"cutting[- ]edge", "delete it"),
    (r"state[- ]of[- ]the[- ]art", "delete it"),
    (r"best[- ]in[- ]class", "delete it"),
    (r"world[- ]class", "delete it"),
    (r"game[- ]chang(er|ers|ing)", "delete it"),
    (r"effortless(ly)?", "say how many steps it takes"),
    (r"supercharg(e|es|ed|ing)", "say what gets faster, and by how much"),
    (r"unleash(es|ed|ing)?", "delete it"),
    (r"delv(e|es|ed|ing)", "use 'read' or 'look at'"),
    (r"DORA[- ]complian(t|ce)", "say 'designed against DORA' and name the article"),
    (r"complian(t|ce)\s+with\s+DORA", "say 'designed against DORA' and name the article"),
    (r"compl(y|ies|ied|ying)\s+with\s+DORA", "say 'designed against DORA' and name the article"),
    (r"meet(s|ing)?\s+DORA", "say 'designed against DORA' and name the article"),
    (r"DORA[- ]conforman(t|ce)", "say 'designed against DORA' and name the article"),
    (r"DORA[- ]ready", "say 'designed against DORA' and name the article"),
    (r"meet(s|ing)?\s+Article", "say 'designed against DORA' and name the article"),
    (r"satisf(y|ies|ied|ying)\s+DORA", "say 'designed against DORA' and name the article"),
    (r"met\s+Article", "say 'designed against DORA' and name the article"),
    (r"satisf(y|ies|ied|ying)\s+Article", "say 'designed against DORA' and name the article"),
    (r"fulfil(s|led|ling)?\s+DORA", "say 'designed against DORA' and name the article"),
    (r"fulfill(s|ed|ing)?\s+DORA", "say 'designed against DORA' and name the article"),
    (r"DORA[- ]certified", "say 'designed against DORA' and name the article"),
]

WORD_RE = re.compile(
    r"(?<![\w-])(" + "|".join(p for p, _ in BANNED) + r")(?![\w-])",
    re.IGNORECASE,
)

# Emoji and pictographs: the main emoji blocks, dingbats, the variation
# selector that turns a text symbol into an emoji, and regional indicators.
EMOJI_RE = re.compile(
    "["
    "\U0001F000-\U0001FAFF"
    "\U00002600-\U000027BF"
    "\U0000FE0F"
    "\U0001F1E6-\U0001F1FF"
    "]"
)

# An accepted ADR changes only in its status line, so a phrase that it
# already holds, in a sentence that denies the claim, stays. Each key is a
# path relative to the repository root, with "/" as separator. Each value
# lists the phrases (lower case) that the file may keep.
EXEMPT = {
    "docs/adr/0147-incident-and-change-records-designed-against-dora.md": {"complies with dora"},
}

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# A line that starts a list item, a table row, a heading or a quote starts a
# new block, so it ends the paragraph before it.
BLOCK_START_RE = re.compile(r"^\s*(?:[-*+]\s|\d+[.)]\s|\||#{1,6}\s|>)")

FENCE_RE = re.compile(r"^\s*(```|~~~)")
CODE_SPAN_RE = re.compile(r"`[^`]*`")


def advice_for(word):
    for pattern, advice in BANNED:
        if re.fullmatch(pattern, word, re.IGNORECASE):
            return advice
    return "delete it"


def repo_path(path):
    """Return the path relative to the repository root, with "/" as separator."""
    return os.path.relpath(os.path.abspath(path), ROOT).replace(os.sep, "/")


def drop_exempt(path, findings):
    """Remove the findings that EXEMPT allows for the file at path."""
    allowed = EXEMPT.get(repo_path(path), ())
    return [f for f in findings if f[2].lower() not in allowed]


def check_text(text):
    """Return a list of (line, column, found, advice) for one document.

    A phrase can wrap across a line break, so the check joins the lines of a
    paragraph before it matches. A blank line, a fence or a line that
    starts a new block ends a paragraph. Each finding keeps the line and
    column of its first character.
    """
    findings = []
    in_fence = False
    block = []  # (line number, prose) of the lines of the open paragraph

    def flush():
        if not block:
            return
        joined = ""
        offsets = []  # (start offset in joined, line number, line start)
        for number, prose in block:
            offsets.append((len(joined), number))
            joined += prose + "\n"
        for match in WORD_RE.finditer(joined):
            start = match.start()
            number = block[0][0]
            base = 0
            for offset, line_number in offsets:
                if offset <= start:
                    number, base = line_number, offset
            word = re.sub(r"\s+", " ", match.group(0))
            findings.append((number, start - base + 1, word, advice_for(word)))
        for match in EMOJI_RE.finditer(joined):
            start = match.start()
            number = block[0][0]
            base = 0
            for offset, line_number in offsets:
                if offset <= start:
                    number, base = line_number, offset
            findings.append((number, start - base + 1, match.group(0), "no emoji"))
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
        prose = CODE_SPAN_RE.sub(lambda m: " " * len(m.group(0)), line)
        block.append((number, prose))
    flush()
    findings.sort()
    return findings


def selftest():
    cases = [
        ("A comprehensive guide.", ["comprehensive"]),
        ("Robust and SEAMLESS.", ["Robust", "SEAMLESS"]),
        ("It is blazingly fast.", ["blazingly fast"]),
        ("A blazing-fast server.", ["blazing-fast"]),
        ("We leverage it.", ["leverage"]),
        ("Ready \U0001F680", ["\U0001F680"]),
        ("Done ✅", ["✅"]),
        ("Use `robust` in code.", []),
        ("```\nrobust\n```\nplain text", []),
        ("The robustness test.", ["robustness"]),
        ("A non-robust-looking word stays.", []),
        ("Leverage-based is not a form we list.", []),
        ("Plain, exact text.", []),
        ("It is DORA compliant.", ["DORA compliant"]),
        ("Proof of dora-compliance.", ["dora-compliance"]),
        ("Designed against DORA, Article 17.", []),
        ("Write `DORA compliant` in a rule.", []),
        ("The server is compliant with DORA.", ["compliant with DORA"]),
        ("It complies with DORA.", ["complies with DORA"]),
        ("This meets DORA in full.", ["meets DORA"]),
        ("A DORA-conformant record.", ["DORA-conformant"]),
        ("Compliance with dora is the goal.", ["Compliance with dora"]),
        ("It meets the DORA article.", []),
        ("A DORA-ready server.", ["DORA-ready"]),
        ("It is dora ready.", ["dora ready"]),
        ("This meets Article 17.", ["meets Article"]),
        ("Each record meets article 9(4)(e).", ["meets article"]),
        ("It satisfies DORA.", ["satisfies DORA"]),
        ("The design satisfied dora in full.", ["satisfied dora"]),
        ("Designed against DORA Article 17, not a claim that it meets the need.", []),
        ("A word like Articles stays.", []),
        ("It complies\nwith DORA.", ["complies with DORA"]),
        ("First line.\nIt is compliant\n  with dora in full.", ["compliant with dora"]),
        ("Ends a paragraph with meets\n\nArticle 5 starts the next one.", []),
        ("The requirement met Article 17.", ["met Article"]),
        ("It satisfies Article 9.", ["satisfies Article"]),
        ("It fulfils DORA. It fulfills dora.", ["fulfils DORA", "fulfills dora"]),
        ("A DORA certified server.", ["DORA certified"]),
        ("- It complies\n- with DORA.", []),
        ("It complies\n- with DORA.", []),
        ("It complies\n| with DORA |", []),
        ("It complies\n# with DORA", []),
        ("It complies\n> with DORA", []),
        ("It complies\n1. with DORA", []),
        ("- It complies\n  with DORA.", ["complies with DORA"]),
    ]
    failed = 0
    wrapped = check_text("a\nb\nIt complies\nwith DORA.")
    if wrapped != [(3, 4, "complies with DORA", advice_for("complies with DORA"))]:
        failed += 1
        print(f"selftest: wrapped position: got {wrapped}", file=sys.stderr)
    root_path = os.path.join(ROOT, "docs", "adr", "0147-incident-and-change-records-designed-against-dora.md")
    other_path = os.path.join(ROOT, "docs", "other", "0147-incident-and-change-records-designed-against-dora.md")
    kept = [(1, 1, "complies with DORA", "")]
    if drop_exempt(root_path, kept) != []:
        failed += 1
        print("selftest: exempt path was not exempt", file=sys.stderr)
    if drop_exempt(other_path, kept) != kept:
        failed += 1
        print("selftest: same file name in another directory was exempt", file=sys.stderr)
    if drop_exempt(os.path.join(ROOT, "0147-incident-and-change-records-designed-against-dora.md"), kept) != kept:
        failed += 1
        print("selftest: bare file name was exempt", file=sys.stderr)
    for text, want in cases:
        got = [f[2] for f in check_text(text)]
        if got != want:
            failed += 1
            print(f"selftest: {text!r}: got {got}, want {want}", file=sys.stderr)
    if failed:
        print(f"selftest: {failed} of {len(cases)} cases failed", file=sys.stderr)
        return 1
    print(f"selftest: {len(cases)} cases passed")
    return 0


def main(argv):
    if argv == ["--selftest"]:
        return selftest()
    if not argv:
        print("usage: docs-banned-words.py FILE... | --selftest", file=sys.stderr)
        return 2
    total = 0
    for path in argv:
        with open(path, encoding="utf-8") as handle:
            findings = check_text(handle.read())
        findings = drop_exempt(path, findings)
        for line, column, found, advice in findings:
            print(f"{path}:{line}:{column} banned word {found!r}: {advice}")
        total += len(findings)
    if total:
        print(f"{total} banned words. Rewrite each line with the advice above.")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
