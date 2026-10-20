// Smoke test of @hey-api/openapi-ts output against transport/fakeserver.py.
import { createClient, createConfig } from "./client/client/index.js";
import { createBackup, deleteBackup, getUnit, listUnits, updatePreferences } from "./client/sdk.gen.js";
import { assert, dustpanFetch, runChecks, seen } from "./transport.js";

const base = process.argv[2] ?? "";
const V1 = "/api/dustpan/v1";
const client = createClient(createConfig({ baseUrl: base, fetch: dustpanFetch("t0k") }));

await runChecks([
  ["get_unit_discriminator_nullable_etag", async () => {
    const r = await getUnit({ client, path: { namespace: "ns", unit_id: "u1" } });
    const u = r.data;
    assert(u !== undefined, "no data");
    assert(u.details.tool === "pulumi" && u.details.stack === "dev", "details");
    assert(u.lock === null && u.resourceCount === null, "nulls");
    assert(r.response?.headers.get("ETag") === '"v1"', "no ETag");
    assert((await seen(base)).requests[`GET ${V1}/namespaces/ns/units/u1`]?.headers.authorization === "Bearer t0k", "auth");
  }],
  ["unknown_enum_and_discriminator", async () => {
    const r = await getUnit({ client, path: { namespace: "ns", unit_id: "u3" } });
    assert(r.data !== undefined, String(r.error));
  }],
  ["unknown_enum_only", async () => {
    const r = await getUnit({ client, path: { namespace: "ns", unit_id: "u5" } });
    assert(r.data !== undefined, String(r.error));
  }],
  ["error_shape", async () => {
    const r = await getUnit({ client, path: { namespace: "ns", unit_id: "u2" } });
    assert(r.response?.status === 404, "status");
    assert(r.error?.errorCode === "unit.not-found" && r.error.errorArgs?.limit === 3, JSON.stringify(r.error));
  }],
  ["pagination", async () => {
    const ids: string[] = [];
    let pageToken: string | undefined;
    for (let i = 0; i < 5; i++) {
      const r = await listUnits({ client, path: { namespace: "ns" }, query: { pageToken } });
      assert(r.data !== undefined, "no data");
      ids.push(...r.data.units.map((u) => u.unitId));
      if (r.data.nextPageToken === "") break;
      pageToken = r.data.nextPageToken;
    }
    assert(ids.join(",") === "u1,u4", ids.join(","));
  }],
  ["merge_patch_null_typed", async () => {
    await updatePreferences({ client, body: { theme: "dark", language: null }, headers: { "If-Match": '"p1"' } });
    const rq = (await seen(base)).requests[`PATCH ${V1}/me/preferences`];
    assert(rq?.headers["content-type"] === "application/merge-patch+json", `content-type ${rq?.headers["content-type"]}`);
    assert(rq.headers["if-match"] === '"p1"' && rq.headers["idempotency-key"] !== undefined, "headers");
    assert(rq.body.includes('"language":null'), rq.body);
  }],
  ["if_match_delete", async () => {
    const r = await deleteBackup({ client, path: { id: "b1" }, headers: { "If-Match": '"b1"' } });
    assert(r.response?.status === 204, "status");
  }],
  ["retry_same_idempotency_key", async () => {
    const r = await createBackup({ client });
    assert(r.data?.id === "b9" && r.data.finishedAt === null, JSON.stringify(r.data));
    const k = (await seen(base)).keys;
    assert(k.length >= 2 && k.at(-1) === k.at(-2), k.join(","));
  }],
]);
