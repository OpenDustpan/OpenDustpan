#!/usr/bin/env python3
"""Print each live Haiku worker session with its state and last action.

Usage: lead_sessions.py <file that holds a saved list_sessions result>
"""

import json
import sys


def main(path):
    text = open(path, encoding="utf-8").read()
    data = json.JSONDecoder().raw_decode(text[text.find('{"'):])[0]
    ccr = data["ccr"]
    items = ccr if isinstance(ccr, list) else ccr.get("data", [])
    for s in items:
        if "ARCHIVED" in s.get("session_status", ""):
            continue
        if s.get("session_context", {}).get("model", "") != "claude-haiku-5-5":
            continue
        meta = s.get("external_metadata") or {}
        summary = meta.get("post_turn_summary") or s.get("post_turn_summary") or {}
        bucket = s.get("status_bucket", "").replace("SESSION_STATUS_BUCKET_", "")
        print(f"{s['id']} {bucket} {s.get('updated_at', '')[:16]} | {s.get('title', '')[:60]}")
        print(f"    last: {summary.get('recent_action', '')[:300]}")
        if summary.get("needs_action"):
            print(f"    NEEDS: {summary['needs_action'][:300]}")


if __name__ == "__main__":
    main(sys.argv[1])
