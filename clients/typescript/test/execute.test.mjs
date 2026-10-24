import assert from "node:assert/strict";
import { test } from "node:test";

import { ApiError, Client, execute, operations } from "../dist/index.js";

const capabilities = { apiVersion: "1", installationKind: "dev" };

const gitHostApp = {
  kind: "github",
  appID: "1",
  apiAddress: "https://api.github.com",
  hookID: "2",
  privateKey: { setID: "a", createdAt: "2026-10-22T19:20:44Z", set: true },
  eventSecret: { setID: "b", createdAt: "2026-10-22T19:20:44Z", set: true },
  createdAt: "2026-10-22T19:20:44Z",
  updatedAt: "2026-10-22T19:20:44Z",
  version: 1,
};

const putApp = {
  path: { kind: "github" },
  body: { appID: "1", apiAddress: "https://api.github.com", privateKey: "k", eventSecret: "s" },
};

function transport(answer) {
  const calls = [];
  return {
    calls,
    send: async (call) => {
      calls.push(call);
      return typeof answer === "function" ? answer(call) : answer;
    },
  };
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

test("an operation with one success status accepts it", async () => {
  const t = transport({ status: 200, body: capabilities, etag: undefined });
  const answer = await new Client(t).getCapabilities();
  assert.equal(answer.status, 200);
  assert.deepEqual(answer.body, capabilities);
  assert.equal(operations.getCapabilities.spec.statuses.length, 1);
});

test("an operation with one success status refuses every other 2xx status", async () => {
  for (const status of [201, 202, 203, 204, 206, 299]) {
    const t = transport({ status, body: capabilities, etag: undefined });
    const error = await rejection(new Client(t).getCapabilities());
    assert.equal(error.kind, "invalid");
    assert.equal(error.status, status);
    assert.match(error.message, new RegExp(`status ${status}`));
  }
});

test("an operation with two success statuses accepts each and refuses every other 2xx status", async () => {
  assert.deepEqual([...operations.putGitHostApp.spec.statuses], [200, 201]);
  for (const status of [200, 201]) {
    const t = transport({ status, body: gitHostApp, etag: '"1"' });
    const answer = await new Client(t).putGitHostApp(putApp);
    assert.equal(answer.status, status);
    assert.equal(answer.etag, '"1"');
    assert.equal(answer.body.kind, "github");
  }
  for (let status = 202; status <= 299; status++) {
    const t = transport({ status, body: gitHostApp, etag: undefined });
    const error = await rejection(new Client(t).putGitHostApp(putApp));
    assert.equal(error.kind, "invalid", `status ${status}`);
    assert.equal(error.status, status);
  }
  for (const status of [204]) {
    const t = transport({ status, body: undefined, etag: undefined });
    const error = await rejection(new Client(t).putGitHostApp(putApp));
    assert.equal(error.kind, "invalid");
  }
});

test("a status that is not a success status is refused even if the transport lets it through", async () => {
  for (const status of [0, 100, 199, 300, 400, 404, 500, 600]) {
    const t = transport({ status, body: capabilities, etag: undefined });
    const error = await rejection(new Client(t).getCapabilities());
    assert.equal(error.kind, "invalid");
  }
});

test("an operation with no body accepts 204 and ignores a body", async () => {
  const t = transport({ status: 204, body: undefined, etag: undefined });
  const answer = await new Client(t).deleteGitHostApp({ path: { kind: "github" } });
  assert.equal(answer.status, 204);
  assert.equal(answer.body, undefined);
  const refused = transport({ status: 200, body: undefined, etag: undefined });
  assert.equal((await rejection(new Client(refused).deleteGitHostApp({ path: { kind: "github" } }))).kind, "invalid");
});

test("an answer that fails validation is refused with its path and its status", async () => {
  const cases = [
    [{ apiVersion: 5 }, /\$\.apiVersion/],
    [{ apiVersion: null, installationKind: "dev" }, /\$\.apiVersion/],
    [{ installationKind: "dev" }, /\$\.apiVersion: the member is missing/],
    [["x"], /want an object/],
    [null, /want an object/],
    ["text", /want an object/],
  ];
  for (const [body, pattern] of cases) {
    const t = transport({ status: 200, body, etag: undefined });
    const error = await rejection(new Client(t).getCapabilities());
    assert.equal(error.kind, "invalid");
    assert.equal(error.status, 200);
    assert.match(error.message, pattern);
    assert.match(error.path, /^\$/);
  }
});

test("an answer with no body is refused where the description has a body", async () => {
  const t = transport({ status: 200, body: undefined, etag: undefined });
  assert.equal((await rejection(new Client(t).getCapabilities())).kind, "invalid");
});

test("a member that the description does not declare is dropped", async () => {
  const t = transport({ status: 200, body: { ...capabilities, surprise: { a: 1 } }, etag: undefined });
  const answer = await new Client(t).getCapabilities();
  assert.deepEqual(answer.body, capabilities);
});

test("an error document from the transport reaches the caller unchanged", async () => {
  const error = new ApiError({
    kind: "server",
    status: 404,
    message: "no sink",
    errorCode: "sink.not-found",
    errorArgs: { id: "x", n: 1 },
  });
  const t = transport(() => {
    throw error;
  });
  const caught = await rejection(new Client(t).getNotificationSink({ path: { namespace: "dev", sink_id: "x" } }));
  assert.equal(caught, error);
});

test("a transport that throws another error gives a transport error", async () => {
  const t = transport(() => {
    throw new TypeError("boom");
  });
  const caught = await rejection(new Client(t).getCapabilities());
  assert.equal(caught.kind, "transport");
  assert.match(caught.message, /boom/);
  assert.ok(caught.cause instanceof TypeError);
});

test("a call that does not fit the operation fails before the transport sees it", async () => {
  const t = transport({ status: 200, body: capabilities, etag: undefined });
  const client = new Client(t);
  const requests = [
    () => execute(t, operations.getNotificationSink, { path: { namespace: "dev" } }),
    () => execute(t, operations.getCapabilities, { ifMatch: '"1"' }),
    () => execute(t, operations.getCapabilities, { idempotencyKey: "k" }),
    () => execute(t, operations.getCapabilities, { body: {} }),
    () => execute(t, operations.getCapabilities, { query: { nope: "1" } }),
    () => execute(t, operations.putGitHostApp, { path: { kind: "github" } }),
    () => client.putGitHostApp({ path: { kind: "github" } }),
  ];
  for (const request of requests) {
    assert.equal((await rejection(request())).kind, "request");
  }
  assert.equal(t.calls.length, 0);
});

test("the transport gets the call as the caller made it", async () => {
  const t = transport({ status: 200, body: gitHostApp, etag: undefined });
  const controller = new AbortController();
  await new Client(t).putGitHostApp({ ...putApp, ifMatch: '"7"', idempotencyKey: "key-1", signal: controller.signal });
  const [call] = t.calls;
  assert.equal(call.spec.id, "putGitHostApp");
  assert.deepEqual(call.path, { kind: "github" });
  assert.equal(call.ifMatch, '"7"');
  assert.equal(call.idempotencyKey, "key-1");
  assert.equal(call.signal, controller.signal);
  assert.deepEqual(call.body, putApp.body);
});

test("query values go to the transport as text", async () => {
  const t = transport({ status: 200, body: { items: [] }, etag: undefined });
  const spec = { ...operations.listNotificationSinks.spec, queryParameters: ["n", "flag", "many", "none"] };
  const op = { spec, decode: undefined };
  await execute(t, op, { path: { namespace: "dev" }, query: { n: 3, flag: true, many: [1, "b"], none: undefined } });
  assert.deepEqual(t.calls[0].query, { n: "3", flag: "true", many: ["1", "b"] });
});

test("the table is frozen", () => {
  assert.throws(() => {
    "use strict";
    operations.getCapabilities.spec.statuses.push(202);
  });
  assert.throws(() => {
    "use strict";
    operations.getCapabilities.spec.id = "x";
  });
});

test("a required If-Match is refused before the request when it is absent", async () => {
  const t = transport({ status: 200, body: {}, etag: undefined });
  const input = { path: { namespace: "dev", profile: "p" }, body: { document: "d", reason: "r" } };
  assert.equal(operations.createProfileRevision.spec.ifMatch, "required");
  const error = await rejection(new Client(t).createProfileRevision(input));
  assert.equal(error.kind, "request");
  assert.match(error.message, /If-Match/);
  assert.equal(t.calls.length, 0);
});

test("a required query parameter is refused before the request when it is absent", async () => {
  const t = transport({ status: 200, body: { sinks: [] }, etag: undefined });
  const spec = { ...operations.listNotificationSinks.spec, queryParameters: ["q"], requiredQueryParameters: ["q"] };
  const op = { spec, decode: undefined };
  const path = { namespace: "dev" };
  for (const query of [undefined, {}, { q: undefined }, { q: [] }]) {
    const error = await rejection(execute(t, op, { path, query }));
    assert.equal(error.kind, "request", JSON.stringify(query));
    assert.match(error.message, /"q"/);
  }
  assert.equal(t.calls.length, 0);
  await execute(t, op, { path, query: { q: "x" } });
  assert.equal(t.calls.length, 1);
});

test("a query value that is not text, a finite number or a boolean is refused", async () => {
  const t = transport({ status: 200, body: { sinks: [] }, etag: undefined });
  const spec = { ...operations.listNotificationSinks.spec, queryParameters: ["q"] };
  const op = { spec, decode: undefined };
  for (const q of [NaN, Infinity, -Infinity, {}, null, [{}], [NaN], () => 1, 10n]) {
    const error = await rejection(execute(t, op, { path: { namespace: "dev" }, query: { q } }));
    assert.equal(error.kind, "request", String(q));
  }
  assert.equal(t.calls.length, 0);
});

test("the package carries the description that its digest names", async () => {
  const { createHash } = await import("node:crypto");
  const { metadata } = await import("../dist/index.js");
  const { description } = await import("@opendustpan/client/description");
  assert.equal(createHash("sha256").update(new TextEncoder().encode(description)).digest("hex"), metadata.descriptionSha256);
  assert.match(metadata.serverCommit, /^(development|[0-9a-f]{40})$/);
});

test("the description is a subpath export and not in the root", async () => {
  const root = await import("@opendustpan/client");
  assert.equal("description" in root, false);
  const sub = await import("@opendustpan/client/description");
  assert.equal(typeof sub.description, "string");
});
