// Smoke test of openapi-typescript types with openapi-fetch.
import createClient from "openapi-fetch";
import type { paths } from "./client/schema.js";
import { assert, dustpanFetch, runChecks, seen } from "./transport.js";

const base = process.argv[2] ?? "";
const V1 = "/api/dustpan/v1";
const c = createClient<paths>({ baseUrl: base, fetch: dustpanFetch("t0k") });
const unit = (id: string) => c.GET("/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}", { params: { path: { namespace: "ns", unit_id: id } } });

await runChecks([
  ["get_unit_discriminator_nullable_etag", async () => {
    const r = await unit("u1");
    const u = r.data;
    assert(u !== undefined, "no data");
    assert(u.details.tool === "pulumi" && u.details.stack === "dev", "details");
    assert(u.lock === null && u.resourceCount === null, "nulls");
    assert(r.response.headers.get("ETag") === '"v1"', "no ETag");
    assert((await seen(base)).requests[`GET ${V1}/namespaces/ns/units/u1`]?.headers.authorization === "Bearer t0k", "auth");
  }],
  ["unknown_enum_and_discriminator", async () => {
    assert((await unit("u3")).data !== undefined, "no data");
  }],
  ["unknown_enum_only", async () => {
    assert((await unit("u5")).data !== undefined, "no data");
  }],
  ["error_shape", async () => {
    const r = await unit("u2");
    assert(r.response.status === 404, "status");
    assert(r.error?.errorCode === "unit.not-found" && r.error.errorArgs?.limit === 3, JSON.stringify(r.error));
  }],
  ["pagination", async () => {
    const ids: string[] = [];
    let pageToken: string | undefined;
    for (let i = 0; i < 5; i++) {
      const r = await c.GET("/api/dustpan/v1/namespaces/{namespace}/units", { params: { path: { namespace: "ns" }, query: { pageToken } } });
      assert(r.data !== undefined, "no data");
      ids.push(...r.data.units.map((u) => u.unitId));
      if (r.data.nextPageToken === "") break;
      pageToken = r.data.nextPageToken;
    }
    assert(ids.join(",") === "u1,u4", ids.join(","));
  }],
  ["merge_patch_null_typed", async () => {
    await c.PATCH("/api/dustpan/v1/me/preferences", {
      params: { header: { "If-Match": '"p1"' } },
      body: { theme: "dark", language: null },
      headers: { "Content-Type": "application/merge-patch+json" },
    });
    const rq = (await seen(base)).requests[`PATCH ${V1}/me/preferences`];
    assert(rq?.headers["content-type"] === "application/merge-patch+json", `content-type ${rq?.headers["content-type"]}`);
    assert(rq.headers["if-match"] === '"p1"' && rq.headers["idempotency-key"] !== undefined, "headers");
    assert(rq.body.includes('"language":null'), rq.body);
  }],
  ["if_match_delete", async () => {
    const r = await c.DELETE("/api/dustpan/v1/backups/{id}", { params: { path: { id: "b1" }, header: { "If-Match": '"b1"' } } });
    assert(r.response.status === 204, `status ${r.response.status}`);
  }],
  ["retry_same_idempotency_key", async () => {
    const r = await c.POST("/api/dustpan/v1/backups", { params: { header: {} } });
    assert(r.data?.id === "b9" && r.data.finishedAt === null, JSON.stringify(r.data));
    const k = (await seen(base)).keys;
    assert(k.length >= 2 && k.at(-1) === k.at(-2), k.join(","));
  }],
]);
