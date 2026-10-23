import assert from "node:assert/strict";
import { test } from "node:test";

import { ApiError, Client, createFetchTransport, dropTokenOffOrigin, metadata, operations, pathSegment } from "../dist/index.js";

const capabilities = { apiVersion: "1", installationKind: "dev" };
const json = { "content-type": "application/json" };

/** A fetch function that answers from a script and records what it got. */
function scripted(...answers) {
  const seen = [];
  const fetch = async (url, init) => {
    seen.push({
      url: String(url),
      method: init.method,
      headers: Object.fromEntries(init.headers),
      body: init.body,
      init,
    });
    const next = answers.length > 1 ? answers.shift() : answers[0];
    return typeof next === "function" ? next(url, init) : next;
  };
  return { seen, fetch };
}

function ok(body = capabilities, init = {}) {
  return new Response(JSON.stringify(body), { status: 200, headers: json, ...init });
}

function client(fetch, options = {}) {
  return new Client(createFetchTransport({ baseUrl: "http://api.test", fetch, ...options }));
}

async function rejection(promise) {
  try {
    await promise;
  } catch (error) {
    assert.ok(error instanceof ApiError, `want an ApiError, got ${error}`);
    return error;
  }
  assert.fail("want an error");
}

test("the request carries the path, the method and the headers of the operation", async () => {
  const s = scripted(ok({ sinks: [] }));
  await client(s.fetch, { token: "tok" }).listNotificationSinks({ path: { namespace: "a/b c%" } });
  const [req] = s.seen;
  assert.equal(req.url, "http://api.test/api/dustpan/v1/namespaces/a%2Fb%20c%25/sinks");
  assert.equal(req.method, "GET");
  assert.equal(req.headers.accept, "application/json");
  assert.equal(req.headers.authorization, "Bearer tok");
  const agent = `opendustpan-client-ts/${metadata.clientVersion} (description ${metadata.descriptionVersion}; server ${metadata.serverRelease})`;
  assert.equal(req.headers["user-agent"], agent);
  assert.equal(req.headers["idempotency-key"], undefined);
  assert.equal(req.body, undefined);
  assert.equal(req.init.redirect, "manual");
});

test("a public operation sends no token", async () => {
  const s = scripted(ok());
  await client(s.fetch, { token: "tok" }).getCapabilities();
  assert.equal(s.seen[0].headers.authorization, undefined);
});

test("an operation that needs a sign-in sends no header when there is no token", async () => {
  const s = scripted(ok({ sinks: [] }));
  await client(s.fetch).listNotificationSinks({ path: { namespace: "dev" } });
  assert.equal(s.seen[0].headers.authorization, undefined);
});

test("a path value that is empty, a dot or two dots is refused before anything is sent", async () => {
  const s = scripted(ok());
  for (const value of ["", ".", ".."]) {
    const error = await rejection(client(s.fetch).listNotificationSinks({ path: { namespace: value } }));
    assert.equal(error.kind, "request", JSON.stringify(value));
  }
  const lone = await rejection(client(s.fetch).listNotificationSinks({ path: { namespace: "\ud800" } }));
  assert.equal(lone.kind, "request");
  assert.equal(s.seen.length, 0);
  assert.equal(pathSegment("n", "..."), "...");
  assert.equal(pathSegment("n", "a..b"), "a..b");
  assert.equal(pathSegment("n", "../x"), "..%2Fx");
});

test("a mutating call sends a valid key, and the key of the caller stays as it is", async () => {
  const body = { name: "p", document: "d" };
  const s = scripted(ok({}));
  const c = client(s.fetch);
  await rejection(c.createProfile({ path: { namespace: "dev" }, body }));
  await rejection(c.createProfile({ path: { namespace: "dev" }, body, idempotencyKey: "key-from-the-caller-1" }));
  assert.match(s.seen[0].headers["idempotency-key"], /^[\x20-\x7e]{1,255}$/);
  assert.equal(s.seen[1].headers["idempotency-key"], "key-from-the-caller-1");
  assert.equal(s.seen[0].headers["content-type"], "application/json");
  assert.equal(s.seen[0].body, JSON.stringify(body));
});

test("a key or an If-Match value with a line break is refused", async () => {
  const s = scripted(ok({}));
  const c = client(s.fetch);
  const body = { name: "p", document: "d" };
  for (const idempotencyKey of ["a\r\nX-Evil: 1", "", "k".repeat(256), "café"]) {
    const error = await rejection(c.createProfile({ path: { namespace: "dev" }, body, idempotencyKey }));
    assert.equal(error.kind, "request", JSON.stringify(idempotencyKey));
  }
  const error = await rejection(
    c.createProfileRevision({
      path: { namespace: "dev", profile: "p" },
      body: { document: "d", reason: "r" },
      ifMatch: '"1"\r\nX-Evil: 1',
    }),
  );
  assert.equal(error.kind, "request");
  assert.equal(s.seen.length, 0);
});

test("a token with a line break is refused", async () => {
  const s = scripted(ok({ sinks: [] }));
  const c = client(s.fetch, { token: "a\r\nX-Evil: 1" });
  const error = await rejection(c.listNotificationSinks({ path: { namespace: "dev" } }));
  assert.equal(error.kind, "request");
  assert.equal(s.seen.length, 0);
});

test("the base URL must be http or https with a host and no query", async () => {
  const bad = [
    "",
    "api.test",
    "ftp://api.test",
    "file:///x",
    "http://api.test/?a=1",
    "http://api.test/#x",
    "http://u:p@api.test",
    "javascript:alert(1)",
  ];
  for (const baseUrl of bad) {
    assert.throws(
      () => createFetchTransport({ baseUrl, fetch: async () => ok() }),
      (e) => e instanceof ApiError && e.kind === "request",
      baseUrl,
    );
  }
  const s = scripted(ok());
  await client(s.fetch, { baseUrl: "https://api.test/prefix/" }).getCapabilities();
  assert.equal(s.seen[0].url, "https://api.test/prefix/api/dustpan/v1/capabilities");
});

test("an error document gives a server error with its code and its arguments", async () => {
  const doc = {
    code: 404,
    message: "no sink",
    errorCode: "sink.not-found",
    errorArgs: { id: "x", n: 1, nested: { a: [1] } },
  };
  const s = scripted(new Response(JSON.stringify(doc), { status: 404, headers: json }));
  const error = await rejection(client(s.fetch).getNotificationSink({ path: { namespace: "dev", sink_id: "x" } }));
  assert.equal(error.kind, "server");
  assert.equal(error.status, 404);
  assert.equal(error.message, "no sink");
  assert.equal(error.errorCode, "sink.not-found");
  assert.deepEqual(error.errorArgs, doc.errorArgs);
});

test("an error body that is not an error document keeps its text", async () => {
  const cases = [
    [new Response("bad gateway", { status: 502, headers: { "content-type": "text/plain" } }), "bad gateway"],
    [new Response("{not json", { status: 500, headers: json }), "{not json"],
    [new Response('["x"]', { status: 500, headers: json }), '["x"]'],
    [new Response('{"message": 5}', { status: 500, headers: json }), '{"message": 5}'],
    [new Response("", { status: 503 }), "status 503"],
  ];
  for (const [response, message] of cases) {
    const error = await rejection(client(scripted(response).fetch).getCapabilities());
    assert.equal(error.kind, "server");
    assert.equal(error.message, message);
    assert.equal(error.errorCode, undefined);
  }
  const long = new Response("x".repeat(5000), { status: 500, headers: { "content-type": "text/plain" } });
  const error = await rejection(client(scripted(long).fetch).getCapabilities());
  assert.equal(error.message.length, 512);
});

test("an error document with a wrong errorCode type keeps the message only", async () => {
  const doc = { code: 400, message: "bad", errorCode: 7, errorArgs: [1] };
  const s = scripted(new Response(JSON.stringify(doc), { status: 400, headers: json }));
  const error = await rejection(client(s.fetch).getCapabilities());
  assert.equal(error.message, "bad");
  assert.equal(error.errorCode, undefined);
  assert.equal(error.errorArgs, undefined);
});

test("a success answer must be JSON", async () => {
  const cases = [
    new Response("{}", { status: 200, headers: { "content-type": "text/html" } }),
    new Response("{}", { status: 200 }),
    new Response("{not json", { status: 200, headers: json }),
    new Response(new Uint8Array([0xff, 0xfe]), { status: 200, headers: json }),
  ];
  for (const response of cases) {
    const error = await rejection(client(scripted(response).fetch).getCapabilities());
    assert.equal(error.kind, "invalid");
    assert.equal(error.status, 200);
  }
  const headers = { "content-type": "Application/JSON; charset=utf-8" };
  const withParameters = new Response(JSON.stringify(capabilities), { status: 200, headers });
  assert.equal((await client(scripted(withParameters).fetch).getCapabilities()).status, 200);
});

test("an answer over the size limit is refused", async () => {
  const big = new Response(JSON.stringify({ apiVersion: "x".repeat(2000) }), { status: 200, headers: json });
  const error = await rejection(client(scripted(big).fetch, { maxResponseBytes: 1000 }).getCapabilities());
  assert.equal(error.kind, "invalid");
  assert.match(error.message, /longer than 1000/);
  const bigError = new Response("x".repeat(2000), { status: 500 });
  const limited = client(scripted(bigError).fetch, { maxResponseBytes: 1000 });
  assert.equal((await rejection(limited.getCapabilities())).kind, "invalid");
});

test("a network failure is a transport error", async () => {
  const fetch = async () => {
    throw new TypeError("fetch failed");
  };
  const error = await rejection(client(fetch).getCapabilities());
  assert.equal(error.kind, "transport");
  assert.equal(error.status, 0);
  assert.match(error.message, /fetch failed/);
});

test("the signal of the caller reaches fetch", async () => {
  const s = scripted(ok());
  const controller = new AbortController();
  await client(s.fetch).getCapabilities({ signal: controller.signal });
  assert.equal(s.seen[0].init.signal, controller.signal);
});

test("a redirect inside the base URL is followed with the token", async () => {
  const redirect = (location, status = 307) => new Response(null, { status, headers: { location } });
  const s = scripted(redirect("/api/dustpan/v1/moved"), redirect("http://api.test/api/dustpan/v1/back"), ok({ sinks: [] }));
  await client(s.fetch, { token: "tok" }).listNotificationSinks({ path: { namespace: "dev" } });
  assert.deepEqual(
    s.seen.map((r) => r.url),
    [
      "http://api.test/api/dustpan/v1/namespaces/dev/sinks",
      "http://api.test/api/dustpan/v1/moved",
      "http://api.test/api/dustpan/v1/back",
    ],
  );
  assert.deepEqual(
    s.seen.map((r) => r.headers.authorization),
    ["Bearer tok", "Bearer tok", "Bearer tok"],
  );
});

test("a redirect that leaves the base origin sends nothing and is a transport error", async () => {
  const targets = [
    "http://other.test/x",
    "https://evil.example.net/steal",
    "http://api.test:8080/x",
    "https://api.test/x",
    "http://127.0.0.1/x",
    "//other.test/x",
    "http://api.test.evil.test/x",
    "http://api.test@evil.test/x",
  ];
  for (const status of [301, 302, 303, 307, 308]) {
    for (const target of targets) {
      const answer = new Response(null, { status, headers: { location: target } });
      const s = scripted(answer, ok({ sinks: [] }));
      const c = client(s.fetch, { token: "tok" });
      const error = await rejection(c.createProfile({ path: { namespace: "dev" }, body: { name: "p", document: "d" } }));
      assert.equal(error.kind, "transport", `${status} ${target}`);
      assert.equal(s.seen.length, 1, `${status} ${target}`);
    }
  }
});

test("a cross-origin 307 never carries the secret body to the other origin", async () => {
  const secret = { appID: "1", apiAddress: "https://api.github.com", privateKey: "-----BEGIN PRIVATE KEY-----", eventSecret: "s" };
  const evil = new Response(JSON.stringify({ kind: "github" }), { status: 200, headers: json });
  const hop = new Response(null, { status: 307, headers: { location: "https://evil.example.net/steal" } });
  const s = scripted(hop, evil);
  const c = new Client(createFetchTransport({ baseUrl: "https://api.example.com", fetch: s.fetch, token: "tok" }));
  const error = await rejection(c.putGitHostApp({ path: { kind: "github" }, body: secret }));
  assert.equal(error.kind, "transport");
  assert.deepEqual(
    s.seen.map((r) => r.url),
    ["https://api.example.com/api/dustpan/v1/git-host-apps/github"],
  );
  assert.ok(s.seen.every((r) => !String(r.url).includes("evil")));
});

test("a redirect outside the base path is a transport error", async () => {
  const s = scripted(new Response(null, { status: 307, headers: { location: "/elsewhere" } }), ok({ sinks: [] }));
  const c = new Client(createFetchTransport({ baseUrl: "http://api.test/prefix", fetch: s.fetch }));
  assert.equal((await rejection(c.getCapabilities())).kind, "transport");
  assert.equal(s.seen.length, 1);
  for (const target of ["/prefixed", "/prefix/../x", "/"]) {
    const t = scripted(new Response(null, { status: 307, headers: { location: target } }), ok());
    const d = new Client(createFetchTransport({ baseUrl: "http://api.test/prefix", fetch: t.fetch }));
    assert.equal((await rejection(d.getCapabilities())).kind, "transport", target);
    assert.equal(t.seen.length, 1, target);
  }
  const inside = scripted(new Response(null, { status: 307, headers: { location: "/prefix/api/dustpan/v1/next" } }), ok());
  const e = new Client(createFetchTransport({ baseUrl: "http://api.test/prefix", fetch: inside.fetch }));
  await e.getCapabilities();
  assert.equal(inside.seen.length, 2);
});

test("an opaque redirect from a browser is a transport error, not a server error with status 0", async () => {
  const opaque = { type: "opaqueredirect", status: 0, headers: new Headers(), body: null };
  const s = scripted(opaque);
  const error = await rejection(client(s.fetch).getCapabilities());
  assert.equal(error.kind, "transport");
  assert.match(error.message, /redirected/);
  assert.equal(s.seen.length, 1);
});

test("a redirect keeps the method for 307 and 308 and becomes a GET for 301, 302 and 303", async () => {
  const body = { name: "p", document: "d" };
  const cases = [
    [307, "POST", true],
    [308, "POST", true],
    [303, "GET", false],
    [302, "GET", false],
    [301, "GET", false],
  ];
  for (const [status, method, keepsBody] of cases) {
    const s = scripted(new Response(null, { status, headers: { location: "/next" } }), ok({}));
    await rejection(client(s.fetch).createProfile({ path: { namespace: "dev" }, body }));
    assert.equal(s.seen[1].method, method, String(status));
    assert.equal(s.seen[1].body !== undefined, keepsBody, String(status));
    assert.equal(s.seen[1].headers["content-type"] !== undefined, keepsBody, String(status));
  }
});

test("a redirect that cannot be followed is a transport error", async () => {
  const loop = () => new Response(null, { status: 307, headers: { location: "/again" } });
  const cases = [
    scripted(loop),
    scripted(new Response(null, { status: 307 })),
    scripted(new Response(null, { status: 307, headers: { location: "javascript:alert(1)" } })),
    scripted(new Response(null, { status: 307, headers: { location: "file:///etc/passwd" } })),
  ];
  for (const s of cases) {
    const error = await rejection(client(s.fetch, { token: "tok" }).getCapabilities());
    assert.equal(error.kind, "transport");
  }
  assert.equal(cases[0].seen.length, 11);
});

test("a 304 or another 3xx without a redirect is a server error", async () => {
  const error = await rejection(client(scripted(new Response(null, { status: 304 })).fetch).getCapabilities());
  assert.equal(error.kind, "server");
  assert.equal(error.status, 304);
});

test("the operation table agrees with the transport on the use of keys", () => {
  assert.equal(operations.createProfile.spec.idempotencyKey, "optional");
  assert.equal(operations.getCapabilities.spec.idempotencyKey, "none");
  assert.equal(operations.getCapabilities.spec.security, "public");
});

// Defense in depth: the redirect check refuses another origin before this
// step runs. These tests reach the step alone, as if that check had failed.
test("the token is dropped on a hop to another origin", () => {
  const headers = new Headers({ authorization: "Bearer secret", accept: "application/json" });
  dropTokenOffOrigin(headers, new URL("https://api.example.com/api/dustpan/v1/a"), new URL("https://evil.example.net/x"));
  assert.equal(headers.has("authorization"), false);
  assert.equal(headers.get("accept"), "application/json");
});

test("the token is dropped on a hop to another port or scheme", () => {
  for (const to of ["https://api.example.com:8443/x", "http://api.example.com/x"]) {
    const headers = new Headers({ authorization: "Bearer secret" });
    dropTokenOffOrigin(headers, new URL("https://api.example.com/a"), new URL(to));
    assert.equal(headers.has("authorization"), false, to);
  }
});

test("the token stays on a hop inside the origin", () => {
  const headers = new Headers({ authorization: "Bearer secret" });
  dropTokenOffOrigin(headers, new URL("https://api.example.com/a"), new URL("https://api.example.com/b"));
  assert.equal(headers.get("authorization"), "Bearer secret");
});
