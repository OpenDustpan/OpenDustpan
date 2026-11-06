#!/usr/bin/env python3
"""Make the hosted Repowise decisions of this repository agree with docs/adr.

scripts/adr-decisions.py makes one entry for each ADR. This script sends the
entries to the hosted Repowise index, so that an agent that asks Repowise gets
the ADRs as accepted decisions:

1. Propose each ADR that the index does not hold, with the propose_decision
   tool of the Repowise MCP endpoint of the repository.
2. Accept each proposed ADR record. Mark an ADR that a later ADR supersedes in
   full as superseded, with a link to the later record.
3. When the text of an ADR changes (for example its status line), retire the
   old record and propose a new one. The new title ends with "(revision
   <hash>)", because the index keeps each title, also of a retired record.
4. Dismiss each candidate that the index found in an ADR file by itself
   ("N. Title"), because the record of step 1 holds the same ADR.

Limits of the hosted index:

- A quote is at most 2000 characters. The record holds the status line, the
  start of the Context section and the start of the Decision section, with
  " [...]" where the text is cut, and names the ADR file.
- The index accepts at most 30 proposals in a minute.
- Steps 2 to 4 use the decision routes of the Repowise dashboard. Repowise
  does not document them. A change on their side can stop this script; the
  script then fails and changes nothing more.

The key comes from the environment variable REPOWISE_API_KEY. The script never
prints it, and sends it only to https://api.repowise.dev.

The evidence of a record is the commit that added the ADR and the last commit
that changed it. The script needs the full history (a clone with
fetch-depth 0).

Usage:
    repowise-sync.py --repo OWNER/NAME [--dry-run]
    repowise-sync.py --selftest

Standard library only.
"""

import hashlib
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
ADR_DIR = ROOT / "docs" / "adr"

# The one host that gets the key. Defense in depth: request() checks the URL
# again before each call, so a new caller cannot send the key elsewhere.
API = "https://api.repowise.dev"
QUOTE_MAX = 2000
CONTEXT_BUDGET = 450
DECISION_MAX = 4000
TITLE_MAX = 200
ELIDE = " [...]"
RESPONSE_MAX = 8 << 20
TIMEOUT = 120
PROPOSE_PAUSE = 2.2
# The REST routes accept 60 requests in a minute. Each request waits for this
# interval after the one before it. A refusal for the rate (HTTP 429) waits
# RETRY_PAUSE and tries again, at most RETRIES times, and then fails.
REQUEST_INTERVAL = 1.1
RETRY_PAUSE = 30
RETRIES = 3
PAGE = 200

REPO = re.compile(r"^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$")
KEY = re.compile(r"^rw_[A-Za-z0-9_-]{16,200}$")
SNAPSHOT = re.compile(r"^[0-9a-f]{6,64}$")
RECORD_ID = re.compile(r"^[0-9a-f]{32}$")
SHA = re.compile(r"^[0-9a-f]{40}$")
ADR_PATH = re.compile(r"^docs/adr/\d{4}-[a-z0-9-]+\.md$")
ADR_TITLE = re.compile(r"^ADR (\d+): ")
LANE_TITLE = re.compile(r"^(\d+)\. ")
REVISION = re.compile(r" \(revision [0-9a-f]{8}\)$")
STATUSES = frozenset({"active", "proposed", "superseded", "deprecated", "dismissed"})


class SyncError(Exception):
    """The index or the input is not in the form that this script expects."""


def clean(text):
    """Remove CR and LF from text for a log line."""
    return str(text).replace("\r", " ").replace("\n", " ")


def load_generator():
    # Loading the generator must not leave a __pycache__ directory in the tree.
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("adr_decisions", ROOT / "scripts" / "adr-decisions.py")
    if spec is None or spec.loader is None:
        raise SyncError("cannot load scripts/adr-decisions.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def check_key(key):
    if not key or not KEY.fullmatch(key):
        raise SyncError("REPOWISE_API_KEY is not set or does not have the form of a Repowise key")
    return key


def check_url(url):
    # Defense in depth: main() builds each URL from API, and this check stays
    # so that a new caller cannot send the key to another host.
    if not (url.startswith(API + "/") and "\r" not in url and "\n" not in url and "@" not in url.split("/")[2]):
        raise SyncError("refused a request to a URL outside " + API)
    return url


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """Refuse each redirect. urllib would send the Authorization header to the
    new host. Defense in depth: check_url() checks only the first URL, so this
    layer stops a redirect to another host on its own."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


OPENER = urllib.request.build_opener(NoRedirect)
_last_request = [0.0]


def request(key, method, path, body=None, accept="application/json"):
    url = check_url(API + path)
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={
        "Content-Type": "application/json", "Accept": accept,
        "Authorization": "Bearer " + check_key(key)})
    for attempt in range(RETRIES + 1):
        wait = _last_request[0] + REQUEST_INTERVAL - time.monotonic()
        if wait > 0:
            time.sleep(wait)
        _last_request[0] = time.monotonic()
        try:
            with OPENER.open(req, timeout=TIMEOUT) as resp:
                raw = resp.read(RESPONSE_MAX + 1)
            break
        except urllib.error.HTTPError as err:
            if err.code == 429 and attempt < RETRIES:
                time.sleep(RETRY_PAUSE)
                continue
            raise SyncError(f"{method} {clean(path)}: HTTP {err.code}") from None
        except (urllib.error.URLError, TimeoutError) as err:
            raise SyncError(f"{method} {clean(path)}: {clean(err)}") from None
    if len(raw) > RESPONSE_MAX:
        raise SyncError(f"{method} {clean(path)}: the response is larger than {RESPONSE_MAX} bytes")
    text = raw.decode("utf-8")
    if text.startswith(("event:", "data:")):
        text = "\n".join(line[5:] for line in text.splitlines() if line.startswith("data:"))
    try:
        return json.loads(text) if text.strip() else None
    except json.JSONDecodeError:
        raise SyncError(f"{method} {clean(path)}: the response is not JSON") from None


def propose(key, repo, args):
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call",
            "params": {"name": "propose_decision", "arguments": args}}
    reply = request(key, "POST", f"/mcp/{repo}", body, "application/json, text/event-stream")
    result = (reply or {}).get("result") or {}
    out = result.get("structuredContent")
    if out is None:
        try:
            out = json.loads(result["content"][0]["text"])
        except (KeyError, IndexError, TypeError, json.JSONDecodeError):
            raise SyncError("propose_decision: no result") from None
    if out.get("error") or result.get("isError"):
        raise SyncError("propose_decision refused " + clean(args["title"]) + ": " + clean(out.get("error")))
    if not RECORD_ID.fullmatch(str(out.get("id", ""))):
        raise SyncError("propose_decision: the reply has no valid id")
    return out


def check_record(rec):
    """Fail closed on a record that does not have the expected form."""
    if not isinstance(rec, dict):
        raise SyncError("a decision record is not an object")
    if not RECORD_ID.fullmatch(str(rec.get("id", ""))):
        raise SyncError("a decision record has no valid id")
    if not isinstance(rec.get("decision_index"), int) or isinstance(rec.get("decision_index"), bool):
        raise SyncError("a decision record has no valid index")
    if rec.get("status") not in STATUSES:
        raise SyncError("a decision record has an unknown status")
    if not isinstance(rec.get("title"), str):
        raise SyncError("a decision record has no title")
    return rec


def snapshot_of(key, repo):
    owner, name = repo.split("/")
    latest = request(key, "GET", f"/repos/{owner}/{name}/latest") or {}
    snap = str(latest.get("short_id", ""))
    if not SNAPSHOT.fullmatch(snap):
        raise SyncError("the index has no ready snapshot of " + clean(repo))
    return snap


def records(key, snap):
    out, offset = [], 0
    while True:
        page = request(key, "GET", f"/snapshots/{snap}/decisions?include_proposed=true"
                       f"&include_dismissed=true&limit={PAGE}&offset={offset}") or {}
        rows = page.get("decisions")
        if not isinstance(rows, list):
            raise SyncError("the decision list has no rows")
        out += [check_record(r) for r in rows]
        offset += PAGE
        if not rows or offset >= int(page.get("total_found", 0)):
            return out


def section(text, name):
    m = re.search(rf"^## {re.escape(name)}\s*\n(.*?)(?=^## |\Z)", text, re.S | re.M)
    return m.group(1).strip() if m else ""


def cut(text, limit):
    """Cut text at a line, a sentence or a word at or below limit."""
    if len(text) <= limit:
        return text, False
    head = text[:limit]
    for sep in ("\n", ". ", " "):
        i = head.rfind(sep)
        if i > limit // 2:
            return head[: i + (1 if sep == ". " else 0)].rstrip(), True
    return head.rstrip(), True


def unwrap(text):
    paras = []
    for p in re.split(r"\n\s*\n", text.strip()):
        paras.append(re.sub(r"[ \t]*\n[ \t]*(?!- |\d+\. )", " ", p.strip()))
    return "\n\n".join(paras)


def git_commits(path):
    if not ADR_PATH.fullmatch(path):
        raise SyncError("not an ADR path: " + clean(path))
    run = ["git", "-C", str(ROOT), "log", "--format=%H", "--", path]
    shas = subprocess.run(run, capture_output=True, text=True, check=True, timeout=60).stdout.split()
    shas = [s for s in shas if SHA.fullmatch(s)]
    if not shas:
        raise SyncError("no commit holds " + path + "; the script needs the full history")
    return list(dict.fromkeys([shas[-1], shas[0]]))


def proposal(entry, commits):
    """The arguments of propose_decision for one entry of adr-decisions.py."""
    adr = entry["accepted_artifact"]
    if not ADR_PATH.fullmatch(adr):
        raise SyncError("not an ADR path: " + clean(adr))
    text = (ROOT / adr).read_text(encoding="utf-8")
    status = next((ln.strip() for ln in text.splitlines() if ln.startswith("Status:")), None)
    if status is None:
        raise SyncError(adr + " has no status line")
    ctx, dec = section(text, "Context"), section(text, "Decision")
    head = status + "\n\n## Context\n\n"
    ctx_part, ctx_cut = cut(ctx, CONTEXT_BUDGET)
    mid = ctx_part + (ELIDE if ctx_cut else "") + "\n\n## Decision\n\n"
    dec_part, dec_cut = cut(dec, QUOTE_MAX - len(head) - len(mid) - len(ELIDE))
    quote = head + mid + dec_part + (ELIDE if dec_cut else "")
    if len(quote) > QUOTE_MAX:
        raise SyncError(adr + ": the quote is too long")
    decision = unwrap(dec_part)
    if dec_cut:
        decision += f" (Shortened. The full decision is in {adr}.)"
    later = re.search(r"\n\nLater changes, from the status line of ADR \d+: (.*)$", entry["decision"], re.S)
    if later:
        decision = f"Status: {later.group(1).strip()} " + decision
    files = [adr] + [s for s in entry.get("scope", []) if s != adr and (ROOT / s).exists()]
    return {
        "title": entry["title"][:TITLE_MAX],
        "decision": decision[:DECISION_MAX],
        "rationale": unwrap(ctx_part)[:DECISION_MAX],
        "source_quote": quote,
        "evidence_commits": commits[:20],
        "affected_files": files[:50],
        "kind": "architectural",
    }


def number(title, pattern):
    m = pattern.match(title)
    return int(m.group(1)) if m else None


def plan(entries, recs):
    """Pair each ADR with its live record. Return (have, lane) maps."""
    have = {}
    for rec in recs:
        n = number(rec["title"], ADR_TITLE)
        if n is None or rec.get("source") != "agent" or rec["status"] in ("dismissed", "deprecated"):
            continue
        prev = have.get(n)
        if prev is None or rec["decision_index"] > prev["decision_index"]:
            have[n] = rec
    numbers = {number(e["title"], ADR_TITLE) for e in entries}
    lane = [r for r in recs if r.get("source") == "adr" and r["status"] == "proposed"
            and number(r["title"], LANE_TITLE) in numbers]
    return have, lane


def sync(key, repo, dry_run):
    gen = load_generator()
    entries = gen.load(ADR_DIR)
    snap = snapshot_of(key, repo)
    have, lane = plan(entries, records(key, snap))
    changes = 0

    def patch(rec, body, why):
        nonlocal changes
        changes += 1
        print(f"{'would set' if dry_run else 'set'} {clean(rec['title'])[:80]}: {json.dumps(body)} ({why})")
        if not dry_run:
            request(key, "PATCH", f"/snapshots/{snap}/decisions/{rec['decision_index']}", body)

    for e in entries:
        n = number(e["title"], ADR_TITLE)
        args = proposal(e, git_commits(e["accepted_artifact"]))
        rec = have.get(n)
        if rec is not None and rec.get("source_quote") == args["source_quote"]:
            continue
        if rec is not None:
            patch(rec, {"status": "deprecated"}, "the ADR text changed")
            digest = hashlib.sha256(args["source_quote"].encode()).hexdigest()[:8]
            args["title"] = REVISION.sub("", args["title"])[: TITLE_MAX - 20] + f" (revision {digest})"
        changes += 1
        print(f"{'would propose' if dry_run else 'propose'} {clean(args['title'])[:80]}")
        if not dry_run:
            out = propose(key, repo, args)
            have[n] = {"id": out["id"], "title": args["title"], "status": out.get("status"),
                       "source_quote": args["source_quote"], "decision_index": None}
            time.sleep(PROPOSE_PAUSE)

    if not dry_run and changes:
        have, lane = plan(entries, records(key, snap))
    for e in entries:
        rec = have.get(number(e["title"], ADR_TITLE))
        if rec is None or rec["decision_index"] is None:
            if not dry_run:
                raise SyncError("the index does not show the record of " + clean(e["title"]))
            continue
        target = have.get(int(e["superseded_by"][4:])) if e.get("superseded_by") else None
        if e.get("currency") == "superseded" and target is not None:
            if rec["status"] != "superseded" or rec.get("superseded_by") != target["id"]:
                patch(rec, {"status": "superseded", "superseded_by": target["id"]}, "superseded in full")
        elif rec["status"] != "active":
            patch(rec, {"status": "active"}, "an accepted ADR")
    for rec in lane:
        patch(rec, {"status": "dismissed"}, "the ADR record holds the same ADR")
    print(f"repowise-sync: {len(entries)} ADRs, {changes} changes{' (dry run)' if dry_run else ''}")


def selftest():
    def refuses(fn, *args):
        try:
            fn(*args)
        except SyncError:
            return True
        return False

    cases = 0
    # Boundary checks: the key, the repository, the record shape.
    for bad in ("", "x", "rw_short", "rw_live_" + "a" * 20 + "\n", "Bearer rw_live_" + "a" * 20):
        assert refuses(check_key, bad), bad
        cases += 1
    assert check_key("rw_live_" + "a" * 32)
    for bad in ("a", "a/b/c", "../x", "a/b\n", "a b/c"):
        assert not REPO.fullmatch(bad), bad
        cases += 1
    for bad in ({}, {"id": "x" * 32}, {"id": "a" * 32, "decision_index": "1", "status": "active", "title": "t"},
                {"id": "a" * 32, "decision_index": 1, "status": "gone", "title": "t"},
                {"id": "a" * 32, "decision_index": True, "status": "active", "title": "t"}):
        assert refuses(check_record, bad), bad
        cases += 1
    # Containment: the key goes to the one host only.
    for bad in ("https://evil.example/x", "http://api.repowise.dev/x", "https://api.repowise.dev.evil.example/x",
                "https://api.repowise.dev@evil.example/x", "https://api.repowise.dev/x\r\nHost: evil"):
        assert refuses(check_url, bad), bad
        cases += 1
    assert check_url(API + "/auth/me")
    # A redirect to another host never carries the key.
    hostile = urllib.request.Request(API + "/x", headers={"Authorization": "Bearer rw_x"})
    assert NoRedirect().redirect_request(hostile, None, 302, "Found", {}, "https://evil.example/y") is None
    cases += 1
    # A request with a bad key fails before any network call.
    assert refuses(request, "", "GET", "/auth/me")
    cases += 1
    # Paths: only ADR files reach git and the file read.
    for bad in ("docs/adr/../../etc/passwd", "/etc/passwd", "docs/adr/0001-x.md; rm -rf /", "docs/rfc/0001-x.md"):
        assert refuses(git_commits, bad), bad
        assert refuses(proposal, {"accepted_artifact": bad, "decision": "", "title": "t"}, []), bad
        cases += 2
    # Log lines lose CR and LF.
    assert clean("a\r\nb") == "a  b"
    cases += 1
    # Quotes stay within the limit of the index, and a cut is marked.
    text, was_cut = cut("word " * 1000, 300)
    assert len(text) <= 300 and was_cut
    cases += 1
    # Pairing: dismissed and retired records do not count; the newest wins.
    recs = [
        {"id": "a" * 32, "decision_index": 1, "status": "dismissed", "title": "ADR 1: x", "source": "agent"},
        {"id": "b" * 32, "decision_index": 2, "status": "active", "title": "ADR 1: x (docs/adr)", "source": "agent"},
        {"id": "c" * 32, "decision_index": 3, "status": "proposed", "title": "1. x", "source": "adr"},
        {"id": "d" * 32, "decision_index": 4, "status": "proposed", "title": "9. y", "source": "adr"},
    ]
    have, lane = plan([{"title": "ADR 1: x"}], recs)
    assert have[1]["id"] == "b" * 32 and [r["id"] for r in lane] == ["c" * 32]
    cases += 1
    # Each ADR gives a proposal that the index takes.
    gen = load_generator()
    entries = gen.load(ADR_DIR)
    for e in entries[:20]:
        args = proposal(e, ["0" * 40])
        assert len(args["source_quote"]) <= QUOTE_MAX and len(args["title"]) <= TITLE_MAX
        assert args["affected_files"][0] == e["accepted_artifact"]
        cases += 1
    print(f"repowise-sync selftest: {cases} cases passed")


def main(argv):
    if argv[1:] == ["--selftest"]:
        selftest()
        return 0
    if len(argv) not in (3, 4) or argv[1] != "--repo" or (len(argv) == 4 and argv[3] != "--dry-run"):
        print("usage: repowise-sync.py --repo OWNER/NAME [--dry-run] | --selftest", file=sys.stderr)
        return 2
    repo = argv[2]
    if not REPO.fullmatch(repo):
        print("repowise-sync: the repository must have the form OWNER/NAME", file=sys.stderr)
        return 2
    try:
        sync(os.environ.get("REPOWISE_API_KEY", ""), repo, len(argv) == 4)
    except (SyncError, subprocess.SubprocessError, OSError) as err:
        print("repowise-sync: " + clean(err), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
