// Smoke test of openapi-generator typescript-fetch output.
import { BackupsApi, Configuration, MeApi, PreferencesThemeEnum, ResponseError, UnitsApi } from "./client/src/index.js";
import { assert, dustpanFetch, runChecks, seen } from "./transport.js";

const base = process.argv[2] ?? "";
const V1 = "/api/dustpan/v1";
const cfg = new Configuration({ basePath: base, fetchApi: dustpanFetch("t0k") });
const units = new UnitsApi(cfg), me = new MeApi(cfg), backups = new BackupsApi(cfg);

await runChecks([
  ["get_unit_discriminator_nullable_etag", async () => {
    const r = await units.getUnitRaw({ namespace: "ns", unitId: "u1" });
    const u = await r.value();
    assert(u.details.tool === "pulumi" && "stack" in u.details && u.details.stack === "dev", "details");
    assert(u.lock === null && u.resourceCount === null, `nulls ${String(u.lock)} ${String(u.resourceCount)}`);
    assert(r.raw.headers.get("ETag") === '"v1"', "no ETag");
    assert((await seen(base)).requests[`GET ${V1}/namespaces/ns/units/u1`]?.headers.authorization === "Bearer t0k", "auth");
  }],
  ["unknown_enum_and_discriminator", async () => {
    await units.getUnit({ namespace: "ns", unitId: "u3" });
  }],
  ["unknown_enum_only", async () => {
    await units.getUnit({ namespace: "ns", unitId: "u5" });
  }],
  ["error_shape", async () => {
    try {
      await units.getUnit({ namespace: "ns", unitId: "u2" });
    } catch (e) {
      assert(e instanceof ResponseError && e.response.status === 404, "status");
      // No typed error model: the caller parses the body by hand.
      const body = (await e.response.json()) as { errorCode?: string; errorArgs?: Record<string, unknown> };
      assert(body.errorCode === "unit.not-found" && body.errorArgs?.limit === 3, JSON.stringify(body));
      return;
    }
    throw new Error("no exception");
  }],
  ["pagination", async () => {
    const ids: string[] = [];
    let pageToken: string | undefined;
    for (let i = 0; i < 5; i++) {
      const l = await units.listUnits({ namespace: "ns", pageToken });
      ids.push(...l.units.map((u) => u.unitId));
      if (l.nextPageToken === "") break;
      pageToken = l.nextPageToken;
    }
    assert(ids.join(",") === "u1,u4", ids.join(","));
  }],
  ["merge_patch_null_typed", async () => {
    await me.updatePreferences({ preferences: { theme: PreferencesThemeEnum.Dark, language: null }, ifMatch: '"p1"' });
    const rq = (await seen(base)).requests[`PATCH ${V1}/me/preferences`];
    assert(rq?.headers["content-type"] === "application/merge-patch+json", `content-type ${rq?.headers["content-type"]}`);
    assert(rq.headers["if-match"] === '"p1"' && rq.headers["idempotency-key"] !== undefined, "headers");
    assert(rq.body.includes('"language":null'), rq.body);
  }],
  ["if_match_delete", async () => {
    await backups.deleteBackup({ id: "b1", ifMatch: '"b1"' });
  }],
  ["retry_same_idempotency_key", async () => {
    const b = await backups.createBackup({});
    assert(b.id === "b9" && b.finishedAt === null, JSON.stringify(b));
    const k = (await seen(base)).keys;
    assert(k.length >= 2 && k.at(-1) === k.at(-2), k.join(","));
  }],
]);
