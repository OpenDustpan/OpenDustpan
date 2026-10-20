"""Smoke test of the two Python clients against transport/fakeserver.py.

Usage: python smoke.py opc|oag <base url>
opc = openapi-python-client, oag = openapi-generator (python).
Prints one line per check: PASS or FAIL with the reason.
"""

import json
import secrets
import sys
import time
import urllib.request

KIND, BASE = sys.argv[1], sys.argv[2]
RESULTS = []


def check(name):
    def wrap(fn):
        try:
            fn()
            RESULTS.append((name, "PASS", ""))
        except Exception as e:  # noqa: BLE001 - the trial reports every failure
            RESULTS.append((name, "FAIL", f"{type(e).__name__}: {e}"[:220]))
        return fn
    return wrap


def seen():
    with urllib.request.urlopen(BASE + "/_seen") as r:
        return json.load(r)


def last(method_path):
    return seen()["requests"][method_path]


V1 = "/api/dustpan/v1"
MUTATING = {"POST", "PUT", "PATCH", "DELETE"}

if KIND == "opc":
    import httpx
    import opendustpan_client as pkg
    from opendustpan_client.api.backups import create_backup, delete_backup
    from opendustpan_client.api.me import update_preferences
    from opendustpan_client.api.units import get_unit, list_units
    from opendustpan_client.models import Error, Preferences, PreferencesTheme, PulumiDetails
    from opendustpan_client.types import UNSET

    class Transport(httpx.BaseTransport):
        """The hand-written transport: an idempotency key and retries."""

        def __init__(self, inner, attempts=3):
            self.inner, self.attempts = inner, attempts

        def handle_request(self, request):
            if request.method in MUTATING and "Idempotency-Key" not in request.headers:
                request.headers["Idempotency-Key"] = secrets.token_hex(16)
            for i in range(self.attempts):
                resp = self.inner.handle_request(request)
                if resp.status_code not in (429, 502, 503, 504) or i == self.attempts - 1:
                    return resp
                resp.close()
                time.sleep(0.01 * 2**i)

    c = pkg.AuthenticatedClient(base_url=BASE, token="t0k", httpx_args={"transport": Transport(httpx.HTTPTransport())})

    @check("get_unit_discriminator_nullable_etag")
    def _():
        r = get_unit.sync_detailed("ns", "u1", client=c)
        u = r.parsed
        assert isinstance(u.details, PulumiDetails) and u.details.stack == "dev", u.details
        assert u.lock is None and u.resource_count is None
        assert r.headers.get("ETag") == '"v1"', "no ETag on the raw answer"
        assert u.additional_properties.get("futureField") == 1, "unknown field lost"
        assert last("GET " + V1 + "/namespaces/ns/units/u1")["headers"]["authorization"] == "Bearer t0k"

    @check("unknown_enum_and_discriminator")
    def _():
        get_unit.sync_detailed("ns", "u3", client=c)

    @check("unknown_enum_only")
    def _():
        get_unit.sync_detailed("ns", "u5", client=c)

    @check("error_shape")
    def _():
        r = get_unit.sync_detailed("ns", "u2", client=c)
        e = r.parsed
        assert isinstance(e, Error) and e.error_code == "unit.not-found" and r.status_code == 404, e
        assert e.error_args["limit"] == 3

    @check("pagination")
    def _():
        ids, tok = [], UNSET
        for _ in range(5):
            page = list_units.sync("ns", client=c, page_token=tok)
            ids += [u.unit_id for u in page.units]
            if not page.next_page_token:
                break
            tok = page.next_page_token
        assert ids == ["u1", "u4"], ids

    @check("merge_patch_null_typed")
    def _():
        update_preferences.sync_detailed(client=c, body=Preferences(theme=PreferencesTheme.DARK, language=None), if_match='"p1"')
        rq = last("PATCH " + V1 + "/me/preferences")
        h = rq["headers"]
        assert h["content-type"] == "application/merge-patch+json" and h["if-match"] == '"p1"' and h.get("idempotency-key"), h
        assert '"language": null' in rq["body"] or '"language":null' in rq["body"], rq["body"]

    @check("if_match_delete")
    def _():
        r = delete_backup.sync_detailed("b1", client=c, if_match='"b1"')
        assert r.status_code == 204, r.status_code

    @check("retry_same_idempotency_key")
    def _():
        b = create_backup.sync(client=c)
        assert b.id == "b9" and b.finished_at is None, b
        k = seen()["keys"]
        assert len(k) >= 2 and k[-1] == k[-2], k

else:
    import opendustpan as pkg
    from opendustpan.exceptions import ApiException

    cfg = pkg.Configuration(host=BASE, access_token="t0k")
    cfg.retries = 3  # urllib3 Retry; it does not retry POST by default
    api = pkg.ApiClient(cfg)

    # The generated client has no transport hook. The idempotency key goes in
    # through a wrapper of call_api, the only place that sees each request.
    orig = api.call_api

    def call_api(method, url, header_params=None, *a, **kw):
        header_params = dict(header_params or {})
        if method in MUTATING and "Idempotency-Key" not in header_params:
            header_params["Idempotency-Key"] = secrets.token_hex(16)
        for i in range(3):
            resp = orig(method, url, header_params, *a, **kw)
            if resp.status not in (429, 502, 503, 504) or i == 2:
                return resp
            time.sleep(0.01 * 2**i)

    api.call_api = call_api
    units, me, backups = pkg.UnitsApi(api), pkg.MeApi(api), pkg.BackupsApi(api)

    @check("get_unit_discriminator_nullable_etag")
    def _():
        r = units.get_unit_with_http_info("ns", "u1")
        u = r.data
        d = u.details.actual_instance
        assert isinstance(d, pkg.PulumiDetails) and d.stack == "dev", d
        assert u.lock is None and u.resource_count is None
        assert r.headers.get("ETag") == '"v1"', "no ETag"
        assert last("GET " + V1 + "/namespaces/ns/units/u1")["headers"]["authorization"] == "Bearer t0k"

    @check("unknown_enum_and_discriminator")
    def _():
        units.get_unit("ns", "u3")

    @check("unknown_enum_only")
    def _():
        units.get_unit("ns", "u5")

    @check("error_shape")
    def _():
        try:
            units.get_unit("ns", "u2")
        except ApiException as e:
            assert e.status == 404
            m = e.data
            assert m is not None, "no typed error model; body is raw text"
            assert m.error_code == "unit.not-found" and m.error_args["limit"].actual_instance == 3, m
            return
        raise AssertionError("no exception")

    @check("pagination")
    def _():
        ids, tok = [], None
        for _ in range(5):
            page = units.list_units("ns", page_token=tok)
            ids += [u.unit_id for u in page.units]
            if not page.next_page_token:
                break
            tok = page.next_page_token
        assert ids == ["u1", "u4"], ids

    @check("merge_patch_null_typed")
    def _():
        me.update_preferences(pkg.Preferences(theme="dark", language=None), if_match='"p1"')
        rq = last("PATCH " + V1 + "/me/preferences")
        h = rq["headers"]
        assert h["content-type"] == "application/merge-patch+json" and h["if-match"] == '"p1"' and h.get("idempotency-key"), h
        assert '"language": null' in rq["body"] or '"language":null' in rq["body"], rq["body"]

    @check("if_match_delete")
    def _():
        backups.delete_backup("b1", '"b1"')

    @check("retry_same_idempotency_key")
    def _():
        b = backups.create_backup()
        assert b.id == "b9" and b.finished_at is None, b
        k = seen()["keys"]
        assert len(k) >= 2 and k[-1] == k[-2], k

for name, status, why in RESULTS:
    print(f"{status} {name} {why}")
