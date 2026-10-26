import assert from "node:assert/strict";
import { test } from "node:test";

import { ApiError, decode, decodeErrorAnswer } from "../dist/index.js";

function invalid(fn, pathPart) {
  assert.throws(fn, (error) => {
    assert.ok(error instanceof ApiError, "want an ApiError");
    assert.equal(error.kind, "invalid");
    assert.equal(error.status, 0);
    if (pathPart !== undefined) {
      assert.ok(error.path === undefined || error.message.includes(pathPart), error.message);
    }
    return true;
  });
}

test("scalars accept their type and refuse the rest, also null", () => {
  const cases = [
    [decode.string, "a", [1, null, true, {}, []]],
    [decode.boolean, false, ["false", 0, null]],
    [decode.number, 1.5, ["1", null, NaN, Infinity]],
    [decode.int64, 7, [1.5, "7", null, 2 ** 53, -(2 ** 53), 1e300]],
  ];
  for (const [d, good, bad] of cases) {
    assert.equal(d(good, "$"), good);
    for (const value of bad) {
      invalid(() => d(value, "$"));
    }
  }
});

test("int64 holds the safe integers and nothing above", () => {
  assert.equal(decode.int64(Number.MAX_SAFE_INTEGER, "$"), Number.MAX_SAFE_INTEGER);
  assert.equal(decode.int64(Number.MIN_SAFE_INTEGER, "$"), Number.MIN_SAFE_INTEGER);
  invalid(() => decode.int64(Number.MAX_SAFE_INTEGER + 1, "$"));
});

test("dateTime accepts the RFC 3339 form and refuses other forms", () => {
  for (const good of [
    "2026-10-22T19:20:44Z",
    "2026-10-22T19:20:44.123456789Z",
    "2026-10-22T19:20:44+02:00",
    "2024-02-29T00:00:00z",
    "2016-12-31T23:59:60Z",
  ]) {
    assert.equal(decode.dateTime(good, "$"), good);
  }
  for (const bad of [
    "2026-10-22",
    "2026-10-22 19:20:44Z",
    "2026-10-22T19:20:44",
    "2026-13-01T00:00:00Z",
    "2026-02-30T00:00:00Z",
    "2025-02-29T00:00:00Z",
    "2026-10-22T24:00:00Z",
    "2026-10-22T19:60:00Z",
    "2026-10-22T19:20:44+24:00",
    "2026-10-22T19:20:44+02:60",
    "2026-10-22T19:20:44Z\n",
    "",
    20261022,
    null,
  ]) {
    invalid(() => decode.dateTime(bad, "$"));
  }
});

test("nullable, array and record carry the path of the wrong value", () => {
  assert.equal(decode.nullable(decode.string)(null, "$"), null);
  assert.equal(decode.nullable(decode.string)("a", "$"), "a");
  invalid(() => decode.nullable(decode.string)(1, "$"));

  const list = decode.array(decode.string);
  assert.deepEqual(list(["a", "b"], "$"), ["a", "b"]);
  assert.throws(() => list(["a", 2], "$.items"), /\$\.items\[1\]/);
  invalid(() => list("a", "$"));
  invalid(() => list(null, "$"));
  invalid(() => list({ length: 0 }, "$"));

  const map = decode.record(decode.int64);
  assert.deepEqual(map({ a: 1 }, "$"), { a: 1 });
  assert.throws(() => map({ "a\"b": "x" }, "$"), /\$\["a\\"b"\]/);
  invalid(() => map([], "$"));
  invalid(() => map(null, "$"));
});

test("a record keeps the key __proto__ as data and does not change the prototype", () => {
  const out = decode.record(decode.string)(JSON.parse('{"__proto__": "x", "b": "y"}'), "$");
  assert.equal(Object.getPrototypeOf(out), Object.prototype);
  assert.equal(Object.hasOwn(out, "__proto__"), true);
  assert.equal(({}).x, undefined);
  assert.equal(out.polluted, undefined);

  const hostile = JSON.parse('{"__proto__": {"polluted": "yes"}}');
  const any = decode.record(decode.anyValue)(hostile, "$");
  assert.equal(Object.getPrototypeOf(any), Object.prototype);
  assert.equal(({}).polluted, undefined);
});

test("closedEnum refuses a value outside the list and openEnum keeps it", () => {
  const closed = decode.closedEnum("State", ["open", "closed"]);
  assert.equal(closed("open", "$"), "open");
  for (const bad of ["Open", "other", "", null, 1, "constructor", "toString"]) {
    invalid(() => closed(bad, "$"));
  }
  const open = decode.openEnum("Status");
  assert.equal(open("new-value", "$"), "new-value");
  invalid(() => open(1, "$"));
  invalid(() => open(null, "$"));
});

test("object keeps the declared members, drops the others and refuses a missing one", () => {
  const widget = decode.object("Widget", {
    id: decode.string,
    note: decode.optional(decode.nullable(decode.string)),
    n: decode.optional(decode.int64),
  });
  assert.deepEqual(widget({ id: "a", extra: 1, __proto__: { x: 1 } }, "$"), { id: "a" });
  assert.deepEqual(widget({ id: "a", note: null, n: 3 }, "$"), { id: "a", note: null, n: 3 });
  assert.throws(() => widget({}, "$"), /\$\.id: the member is missing/);
  assert.throws(() => widget({ id: null }, "$"), /\$\.id/);
  assert.throws(() => widget({ id: "a", n: "3" }, "$"), /\$\.n/);
  assert.throws(() => widget({ id: "a", n: null }, "$"), /\$\.n/);
  for (const bad of [null, [], "a", 1, undefined]) {
    invalid(() => widget(bad, "$"));
  }
});

test("object does not read a member from the prototype", () => {
  const widget = decode.object("Widget", { toString: decode.string });
  invalid(() => widget({}, "$"));
});

test("ref allows a schema that refers to itself", () => {
  const node = decode.object("Node", {
    next: decode.optional(decode.nullable(decode.ref(() => node))),
  });
  assert.deepEqual(node({ next: { next: null } }, "$"), { next: { next: null } });
  assert.throws(() => node({ next: { next: 1 } }, "$"), /\$\.next\.next/);
});

test("union picks the decoder of a named value and gives the base the rest", () => {
  const base = decode.object("Base", { kind: decode.string, n: decode.optional(decode.int64) });
  const named = decode.object("Named", { kind: decode.literal("a"), n: decode.int64 });
  const pick = decode.union("Pick", "kind", { a: named }, base);
  assert.deepEqual(pick({ kind: "a", n: 1 }, "$"), { kind: "a", n: 1 });
  assert.throws(() => pick({ kind: "a" }, "$"), /\$\.n: the member is missing/);
  assert.deepEqual(pick({ kind: "b" }, "$"), { kind: "b" });
  for (const hostile of ["__proto__", "constructor", "toString", "hasOwnProperty"]) {
    assert.deepEqual(pick({ kind: hostile }, "$"), { kind: hostile });
  }
  invalid(() => pick({ kind: 1 }, "$"));
  invalid(() => pick({}, "$"));
  for (const bad of [null, [], "a", 1, undefined]) {
    invalid(() => pick(bad, "$"));
  }
});

test("literal accepts its one value and refuses every other value", () => {
  const one = decode.literal("lock.locked");
  assert.equal(one("lock.locked", "$"), "lock.locked");
  for (const bad of ["lock.other", "", "LOCK.LOCKED", null, 1, {}]) {
    invalid(() => one(bad, "$"));
  }
});

const lockedArgs = {
  holder: "alice",
  since: "2026-10-22T19:20:44Z",
  lockedKind: "unit",
  locked: "u1",
  refusedKind: "unit",
  refused: "u1",
};

test("decodeErrorAnswer gives a code with arguments its own typed shape", () => {
  const answer = decodeErrorAnswer({
    code: 423,
    message: "locked",
    errorCode: "lock.locked",
    errorArgs: { ...lockedArgs, extra: 1 },
  }, "$");
  assert.deepEqual(answer.errorArgs, lockedArgs);
  assert.equal(answer.errorCode, "lock.locked");
});

test("decodeErrorAnswer refuses a known code with malformed arguments", () => {
  const answer = { code: 423, message: "locked", errorCode: "lock.locked" };
  invalid(() => decodeErrorAnswer(answer, "$"), "errorArgs");
  invalid(() => decodeErrorAnswer({ ...answer, errorArgs: {} }, "$"), "holder");
  invalid(() => decodeErrorAnswer({ ...answer, errorArgs: { ...lockedArgs, holder: 1 } }, "$"), "holder");
  invalid(() => decodeErrorAnswer({ ...answer, errorArgs: { ...lockedArgs, since: "yesterday" } }, "$"), "since");
  invalid(() => decodeErrorAnswer({ ...answer, errorArgs: null }, "$"), "errorArgs");
  invalid(() => decodeErrorAnswer({ ...answer, code: "423", errorArgs: lockedArgs }, "$"), "code");
});

test("decodeErrorAnswer keeps an unknown code and a code with no arguments as the base shape", () => {
  const free = { code: 500, message: "m", errorCode: "future.code", errorArgs: { anything: [1, "a"] } };
  assert.deepEqual(decodeErrorAnswer(free, "$"), free);
  const none = { code: 404, message: "m", errorCode: "lock.not-locked" };
  assert.deepEqual(decodeErrorAnswer(none, "$"), none);
  const plain = { code: 404, message: "m" };
  assert.deepEqual(decodeErrorAnswer(plain, "$"), plain);
  const proto = { code: 400, message: "m", errorCode: "__proto__" };
  assert.deepEqual(decodeErrorAnswer(proto, "$"), proto);
  invalid(() => decodeErrorAnswer({ code: 400, message: "m", errorCode: 7 }, "$"));
});
