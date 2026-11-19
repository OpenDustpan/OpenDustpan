#!/usr/bin/env python3
"""Print the toil and churn signals of a Git repository.

The script reads the Git history and the working tree. It changes nothing.
It needs Python 3.9 or later and git. It uses no other package.

    churn.py [--repo DIR] [--ref REF] [--since DATE] [--top N]

Each section of the output is a signal, not a finding. A person or an agent
must read the commits behind a number before it becomes a finding.
"""

from __future__ import annotations

import argparse
import collections
import itertools
import re
import subprocess
import sys
from pathlib import Path

# A commit that touches more files than this is a bulk move or a format run.
# It says nothing about which files change together.
BULK_COMMIT_FILES = 60

SUBJECT_SIGNALS = {
    "fix": r"\bfix(es|ed)?\b",
    "revert": r"\brevert",
    "again / once more": r"\bagain\b|\bonce more\b|\bre-?do\b",
    "follow-up / review": r"follow-?up|\breview\b|\bnits?\b",
    "flaky / race / timeout": r"flak|\brace\b|timeout|\bslow\b|deadlock|\bretry\b",
    "conflict / merge / rebase": r"conflict|\bmerge\b|rebase",
    "regenerate / golden / snapshot": r"regenerat|\bgolden\b|snapshot",
    "lint / format": r"\blint|\bfmt\b|format",
    "ci / workflow / cache": r"^ci\b|\bci:|workflow|\bcache\b|runner",
    "bump / pin / upgrade": r"\bbump|\bpin\b|upgrade",
    "move / rename / split": r"\bmove|\brename|\bsplit\b",
    "remove / drop": r"\bremove|\bdrop\b|\bdelete",
}

# Markers that switch a check off for one line or one file.
SUPPRESSIONS = [
    r"//\s*nolint:[a-z0-9_,-]+", r"nosemgrep", r"eslint-disable[a-z-]*",
    r"@ts-(?:ignore|expect-error)", r"#\s*noqa", r"#\s*type:\s*ignore",
    r"#\[allow\([a-z_:]+\)", r"@SuppressWarnings", r"rubocop:disable",
    r"\bt\.Skip\(|\bit\.skip\(|@pytest\.mark\.skip|#\[ignore\]",
]

CONFIG_RE = re.compile(
    r"^(\.[^/]+|Makefile|Justfile|Taskfile\.ya?ml|Dockerfile[^/]*|[^/]+\.(ya?ml|toml|json|cfg|ini|mod|lock)"
    r"|\.github/.+|\.circleci/.+|scripts/.+|[^/]*(AGENTS|CLAUDE|CONTRIBUTING)\.md)$"
)
GENERATED_PATH_RE = re.compile(
    r"(^|/)(testdata|__snapshots__|golden|vendor|node_modules|dist|gen|generated)/"
    r"|\.(golden|snap|lock|pb\.go|min\.js)$|(^|/)(go\.sum|package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$"
)
GENERATED_HEADER_RE = re.compile(rb"Code generated .* DO NOT EDIT|@generated|DO NOT EDIT", re.I)
DOC_RE = re.compile(r"\.(md|mdx|rst|adoc|txt)$")


def git(repo: Path, *args: str) -> str:
    """Run git with an argument list and return its output."""
    done = subprocess.run(
        ["git", "-C", str(repo), *args], capture_output=True, text=True, errors="replace", check=False
    )
    if done.returncode != 0:
        sys.exit(f"churn: git {args[0]} failed: {done.stderr.strip()}")
    return done.stdout


def section(title: str) -> None:
    print(f"\n## {title}\n")


def percentile(values: list[int], share: float) -> int:
    return values[min(len(values) - 1, int(len(values) * share))] if values else 0


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo", default=".", type=Path)
    parser.add_argument("--ref", default="HEAD", help="branch to read; use the default branch, for example origin/main")
    parser.add_argument("--since", default=None, help="for example '3 months ago'")
    parser.add_argument("--top", default=25, type=int)
    args = parser.parse_args()
    repo, top = args.repo, args.top

    if git(repo, "rev-parse", "--is-shallow-repository").strip() == "true":
        print("WARNING: this clone is shallow. The numbers cover only a part of the history.")
        print("Run `git fetch --unshallow` (or `git fetch --deepen=5000 origin <branch>`) and start again.")

    log_args = ["log", args.ref, "--no-merges", "--no-renames", "--numstat", "--date=short", "--format=@%h\t%ad\t%an\t%s"]
    if args.since:
        log_args.append(f"--since={args.since}")

    commits: list[dict] = []
    for line in git(repo, *log_args).splitlines():
        if line.startswith("@"):
            sha, day, author, subject = line[1:].split("\t", 3)
            commits.append({"sha": sha, "day": day, "author": author, "subject": subject, "files": {}})
        elif line.strip() and commits:
            added, deleted, path = line.split("\t", 2)
            lines = 0 if added == "-" else int(added) + int(deleted)
            commits[-1]["files"][path] = lines
    if not commits:
        sys.exit("churn: no commits in this range")

    tracked = set(git(repo, "ls-files").splitlines())
    touches = collections.Counter(path for c in commits for path in c["files"])
    days = sorted({c["day"] for c in commits})

    section("Overview")
    per_day = collections.Counter(c["day"] for c in commits)
    sizes = sorted(len(c["files"]) for c in commits)
    print(f"- commits: {len(commits)} from {days[0]} to {days[-1]} on {len(days)} active days")
    print(f"- busiest day: {per_day.most_common(1)[0][1]} commits; median day: {percentile(sorted(per_day.values()), 0.5)}")
    print(f"- files in one commit: median {percentile(sizes, 0.5)}, p90 {percentile(sizes, 0.9)}, max {sizes[-1]}")
    authors = collections.Counter(c["author"] for c in commits)
    print("- authors: " + ", ".join(f"{name} ({n})" for name, n in authors.most_common(6)))

    section("Hot files (commits that touch the file)")
    print("A hot file that many changes must edit is a conflict point. Ask why each change needs it.\n")
    file_days = collections.defaultdict(set)
    for c in commits:
        for path in c["files"]:
            file_days[path].add(c["day"])
    for path, n in touches.most_common(top):
        gone = "" if path in tracked else "  (no longer in the tree)"
        print(f"{n:5d} commits on {len(file_days[path]):3d} days  {path}{gone}")

    section("Files that change together")
    print("share = commits with both files / commits of the less frequent file. A high share between")
    print("hand-written files is one decision written in two places.\n")
    floor = max(5, len(commits) // 100)
    hot = {path for path, n in touches.items() if n >= floor}
    pairs: collections.Counter = collections.Counter()
    for c in commits:
        if len(c["files"]) > BULK_COMMIT_FILES:
            continue
        for pair in itertools.combinations(sorted(hot & c["files"].keys()), 2):
            pairs[pair] += 1
    rows = [(n / min(touches[a], touches[b]), n, a, b) for (a, b), n in pairs.items() if n >= floor]
    for share, n, a, b in sorted((r for r in rows if r[0] >= 0.5), reverse=True)[:top]:
        print(f"{share:.2f} {n:4d}  {a}  <->  {b}")

    section("Rework in one day (commits on one file in one day)")
    print("Many commits on one file in one day show a slow feedback loop or a serial queue on that file.\n")
    same_day = collections.Counter((c["day"], path) for c in commits for path in c["files"])
    for (day, path), n in same_day.most_common(top // 2):
        if n >= 4:
            print(f"{n:4d}  {day}  {path}")

    section("Commit subjects")
    print("The count is the number of subjects that match. Read the samples: a word can be innocent.\n")
    for name, pattern in SUBJECT_SIGNALS.items():
        matched = [c for c in commits if re.search(pattern, c["subject"], re.I)]
        if not matched:
            continue
        print(f"{len(matched):5d} ({100 * len(matched) / len(commits):.0f}%)  {name}")
        for c in matched[:3]:
            print(f"         {c['day']} {c['subject'][:110]}")
    prefixes = collections.Counter(
        re.split(r"[:\s(]", c["subject"].lower(), maxsplit=1)[0] for c in commits
    )
    print("\nMost frequent first words: " + ", ".join(f"{w} ({n})" for w, n in prefixes.most_common(15)))

    section("Configuration, tooling and rule files")
    print("Each change here is work on the workshop and not on the product. Growth of a rule file shows")
    print("that a problem became a rule for people and not a mechanism.\n")
    config = [(n, path) for path, n in touches.items() if CONFIG_RE.match(path)]
    for n, path in sorted(config, reverse=True)[:top]:
        size = ""
        if path in tracked and (repo / path).is_file():
            now = len((repo / path).read_bytes().splitlines())
            first_sha = git(repo, "log", args.ref, "--diff-filter=A", "--format=%h", "--", path).split()
            if first_sha:
                then = len(git(repo, "show", f"{first_sha[-1]}:{path}").splitlines())
                size = f"  lines: {then} -> {now}"
        print(f"{n:5d}  {path}{size}")

    section("Where the changed lines go")
    total = generated = docs = 0
    header_cache: dict[str, bool] = {}

    def is_generated(path: str) -> bool:
        if GENERATED_PATH_RE.search(path):
            return True
        if path not in header_cache:
            file = repo / path
            head = file.read_bytes()[:400] if path in tracked and file.is_file() else b""
            header_cache[path] = bool(GENERATED_HEADER_RE.search(head))
        return header_cache[path]

    for c in commits:
        for path, lines in c["files"].items():
            total += lines
            if is_generated(path):
                generated += lines
            elif DOC_RE.search(path):
                docs += lines
    if total:
        print(f"- all changed lines: {total}")
        print(f"- generated, golden, snapshot and lock files: {generated} ({100 * generated / total:.0f}%)")
        print(f"- prose (documents, specs, rule files): {docs} ({100 * docs / total:.0f}%)")

    section("Conflict magnets (hot files with one very long line)")
    print("Git merges by line. Two changes to one long line always conflict.\n")
    for path, n in touches.most_common(200):
        file = repo / path
        if path in tracked and file.is_file() and file.stat().st_size < 20_000_000:
            longest = max((len(line) for line in file.read_bytes().splitlines()), default=0)
            if longest > 2000:
                print(f"{n:5d} commits, longest line {longest} bytes  {path}")

    section("Short-lived files (added and deleted in this range)")
    born: dict[str, str] = {}
    died: list[tuple[str, str, str]] = []
    status_args = ["log", args.ref, "--no-merges", "--reverse", "--no-renames", "--diff-filter=AD", "--name-status", "--date=short", "--format=@%ad"]
    if args.since:
        status_args.append(f"--since={args.since}")
    day = ""
    for line in git(repo, *status_args).splitlines():
        if line.startswith("@"):
            day = line[1:]
        elif line.startswith("A\t"):
            born[line[2:]] = day
        elif line.startswith("D\t") and line[2:] in born:
            died.append((born.pop(line[2:]), day, line[2:]))
    by_dir = collections.Counter(str(Path(path).parent) for _, _, path in died)
    print(f"{len(died)} files. Directories with the most:")
    for directory, n in by_dir.most_common(10):
        print(f"{n:5d}  {directory}")

    section("Suppression markers in the tree")
    print("A check with many suppressions has the wrong configuration for this code.\n")
    found: collections.Counter = collections.Counter()
    pattern = re.compile("|".join(f"(?:{s})" for s in SUPPRESSIONS))
    for path in tracked:
        file = repo / path
        if GENERATED_PATH_RE.search(path) or not file.is_file() or file.stat().st_size > 2_000_000:
            continue
        try:
            text = file.read_text(errors="ignore")
        except OSError:
            continue
        found.update(m.group(0).strip() for m in pattern.finditer(text))
    for marker, n in found.most_common(top):
        print(f"{n:5d}  {marker}")


if __name__ == "__main__":
    main()
