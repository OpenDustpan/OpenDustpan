"""Fake server for the Python and TypeScript smoke tests (standard library only).

It answers the same shapes as the Go fake server in transport/go/smoke.
GET /_seen returns what it received, so a test can check headers and bodies.
Run: python3 -I fakeserver.py <port>
"""

import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

V1 = "/api/dustpan/v1"
SEEN = {"requests": {}, "keys": [], "tries": {}}
LOCK = threading.Lock()


def unit(uid, tool, details):
    return {
        "unitId": uid, "namespace": "ns", "name": "app", "tool": tool,
        "lastChange": "2026-10-18T09:30:00Z", "resourceCount": None, "lock": None,
        "labels": {"team": "a"}, "details": details, "futureField": 1,
    }


PULUMI = {"tool": "pulumi", "project": "p", "stack": "dev"}


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def send(self, status, body=None, headers=None):
        self.send_response(status)
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        if body is None:
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        b = json.dumps(body).encode()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def record(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n).decode() if n else ""
        path = self.path.split("?")[0]
        k = f"{self.command} {path}"
        with LOCK:
            SEEN["requests"][k] = {"headers": {h.lower(): v for h, v in self.headers.items()}, "body": body}
            SEEN["tries"][k] = SEEN["tries"].get(k, 0) + 1
            if self.command == "POST":
                SEEN["keys"].append(self.headers.get("Idempotency-Key"))
            return SEEN["tries"][k]

    def do_GET(self):
        if self.path == "/_seen":
            with LOCK:
                return self.send(200, SEEN)
        self.record()
        path, _, query = self.path.partition("?")
        if path == V1 + "/namespaces/ns/units/u1":
            return self.send(200, unit("u1", "pulumi", PULUMI), {"ETag": '"v1"'})
        if path == V1 + "/namespaces/ns/units/u3":
            return self.send(200, unit("u3", "ansible", {"tool": "ansible", "playbook": "x"}))
        if path == V1 + "/namespaces/ns/units/u5":
            return self.send(200, unit("u5", "ansible", PULUMI))
        if path == V1 + "/namespaces/ns/units/u2":
            return self.send(404, {"code": 404, "message": "The unit u2 does not exist.", "errorCode": "unit.not-found",
                                   "errorArgs": {"unitId": "u2", "limit": 3}})
        if path == V1 + "/namespaces/ns/units":
            last = "pageToken=p2" in query
            u = unit("u4" if last else "u1", "opentofu", {"tool": "opentofu", "workspace": "w", "terraformVersion": None})
            return self.send(200, {"units": [u], "nextPageToken": "" if last else "p2"})
        self.send(404, {"code": 404, "message": "No route."})

    def do_PATCH(self):
        self.record()
        if self.path == V1 + "/me/preferences":
            return self.send(200, {"theme": "dark", "language": None}, {"ETag": '"p2"'})
        self.send(404, {"code": 404, "message": "No route."})

    def do_DELETE(self):
        self.record()
        if self.path == V1 + "/backups/b1":
            if self.headers.get("If-Match") != '"b1"':
                return self.send(412, {"code": 412, "message": "The ETag does not match.", "errorCode": "request.precondition-failed"})
            return self.send(204)
        self.send(404, {"code": 404, "message": "No route."})

    def do_POST(self):
        n = self.record()
        if self.path.startswith(V1 + "/backups"):
            if n == 1:
                return self.send(503, {"code": 503, "message": "Try again later."})
            return self.send(202, {"id": "b9", "state": "pending", "createdAt": "2026-10-18T09:30:00Z", "finishedAt": None})
        self.send(404, {"code": 404, "message": "No route."})


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
