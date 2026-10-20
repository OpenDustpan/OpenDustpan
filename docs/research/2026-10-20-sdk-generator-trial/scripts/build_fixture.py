"""Build the trial fixture description in OpenAPI 3.0.3 and 3.1.0.

The fixture is NOT the API description of the server. No description exists
on main yet (slice 1 is open). The fixture uses the shapes that the accepted
ADRs fix for the tool-neutral API: paths, cursor lists, Idempotency-Key,
If-Match and ETag, JSON Merge Patch, the error shape with errorCode and
errorArgs, and the capabilities document. Each construct that the trial
checks appears at least once.

Run: python3 -I build_fixture.py <out dir>
"""

import copy
import json
import sys

P = "/api/dustpan/v1"


def ref(name):
    return {"$ref": "#/components/schemas/" + name}


def p(name):
    return {"$ref": "#/components/parameters/" + name}


def resp(name):
    return {"$ref": "#/components/responses/" + name}


def json_body(schema, media="application/json"):
    return {"content": {media: {"schema": schema}}}


ERRORS = {"default": resp("Error")}
MUTATING = [p("IdempotencyKey")]
ETAG_HEADER = {"ETag": {"$ref": "#/components/headers/ETag"}}


def op(op_id, summary, actions, *, params=None, body=None, responses, public=False, tags=None):
    o = {
        "operationId": op_id,
        "summary": summary,
        "tags": tags or [],
        "x-dustpan-actions": actions,
        "responses": {**responses, **ERRORS},
    }
    if params:
        o["parameters"] = params
    if body:
        o["requestBody"] = body
    if public:
        o["security"] = []
    return o


schemas = {
    "Error": {
        "type": "object",
        "description": "The error shape of the tool-neutral API. code is the HTTP status.",
        "required": ["code", "message"],
        "properties": {
            "code": {"type": "integer", "format": "int32", "example": 404},
            "message": {"type": "string"},
            "errorCode": {"type": "string", "pattern": r"^[a-z][a-z0-9]*(-[a-z0-9]+)*(\.[a-z][a-z0-9]*(-[a-z0-9]+)*)+$"},
            "errorArgs": {
                "type": "object",
                "maxProperties": 8,
                "additionalProperties": {"oneOf": [{"type": "string", "maxLength": 256}, {"type": "integer", "format": "int64"}]},
            },
        },
    },
    "Tool": {"type": "string", "enum": ["pulumi", "opentofu"]},
    "Module": {
        "type": "object",
        "required": ["name", "enabled"],
        "properties": {"name": {"type": "string"}, "enabled": {"type": "boolean"}},
    },
    "Capabilities": {
        "type": "object",
        "required": ["apiVersion"],
        "properties": {
            "apiVersion": {"type": "string", "example": "v1"},
            "modules": {"type": "array", "items": ref("Module")},
        },
    },
    "Lock": {
        "type": "object",
        "required": ["holder", "since", "operation"],
        "properties": {
            "holder": {"type": "string"},
            "since": {"type": "string", "format": "date-time"},
            "operation": {"type": "string"},
        },
    },
    "PulumiDetails": {
        "type": "object",
        "required": ["tool", "project", "stack"],
        "properties": {"tool": {"type": "string", "enum": ["pulumi"]}, "project": {"type": "string"}, "stack": {"type": "string"}},
    },
    "OpenTofuDetails": {
        "type": "object",
        "required": ["tool", "workspace"],
        "properties": {
            "tool": {"type": "string", "enum": ["opentofu"]},
            "workspace": {"type": "string"},
            "terraformVersion": {"type": "string", "nullable": True},
        },
    },
    "UnitDetails": {
        "oneOf": [ref("PulumiDetails"), ref("OpenTofuDetails")],
        "discriminator": {
            "propertyName": "tool",
            "mapping": {"pulumi": "#/components/schemas/PulumiDetails", "opentofu": "#/components/schemas/OpenTofuDetails"},
        },
    },
    "Unit": {
        "type": "object",
        "required": ["unitId", "namespace", "name", "tool", "lastChange", "details"],
        "properties": {
            "unitId": {"type": "string"},
            "namespace": {"type": "string"},
            "name": {"type": "string"},
            "tool": ref("Tool"),
            "lastChange": {"type": "string", "format": "date-time"},
            "resourceCount": {"type": "integer", "format": "int64", "nullable": True},
            "lock": {"allOf": [ref("Lock")], "nullable": True},
            "labels": {"type": "object", "additionalProperties": {"type": "string"}},
            "details": ref("UnitDetails"),
        },
    },
    "UnitList": {
        "type": "object",
        "required": ["units", "nextPageToken"],
        "properties": {"units": {"type": "array", "items": ref("Unit")}, "nextPageToken": {"type": "string"}},
    },
    "BackupState": {"type": "string", "enum": ["pending", "running", "succeeded", "failed"]},
    "Backup": {
        "type": "object",
        "required": ["id", "state", "createdAt"],
        "properties": {
            "id": {"type": "string"},
            "state": ref("BackupState"),
            "createdAt": {"type": "string", "format": "date-time"},
            "finishedAt": {"type": "string", "format": "date-time", "nullable": True},
            "sizeBytes": {"type": "integer", "format": "int64", "nullable": True},
        },
    },
    "BackupCreate": {
        "type": "object",
        "properties": {"note": {"type": "string", "maxLength": 256}},
        "additionalProperties": False,
    },
    "BackupList": {
        "type": "object",
        "required": ["backups", "nextPageToken"],
        "properties": {"backups": {"type": "array", "items": ref("Backup")}, "nextPageToken": {"type": "string"}},
    },
    "BackupCheck": {
        "type": "object",
        "required": ["ok", "problems"],
        "properties": {"ok": {"type": "boolean"}, "problems": {"type": "array", "items": {"type": "string"}}},
    },
    "Preferences": {
        "type": "object",
        "properties": {
            "theme": {"type": "string", "enum": ["system", "light", "dark"]},
            "language": {"type": "string", "nullable": True},
            "pinnedUnits": {"type": "array", "items": {"type": "string"}},
            "extra": {"type": "object", "additionalProperties": True},
        },
    },
    "Verdict": {
        "type": "object",
        "required": ["decision"],
        "properties": {
            "decision": {"type": "string", "enum": ["approve", "reject"]},
            "comment": {"type": "string", "nullable": True},
        },
        "additionalProperties": False,
    },
    "ChangeOrder": {
        "type": "object",
        "required": ["id", "status"],
        "properties": {"id": {"type": "string"}, "status": {"type": "string", "enum": ["open", "approved", "rejected", "applied"]}},
    },
}

parameters = {
    "Namespace": {"name": "namespace", "in": "path", "required": True, "schema": {"type": "string"}, "description": "A namespace name, or - for a read across namespaces."},
    "UnitId": {"name": "unit_id", "in": "path", "required": True, "schema": {"type": "string"}},
    "Id": {"name": "id", "in": "path", "required": True, "schema": {"type": "string"}},
    "PageSize": {"name": "pageSize", "in": "query", "schema": {"type": "integer", "format": "int32", "minimum": 1, "maximum": 500, "default": 50}},
    "PageToken": {"name": "pageToken", "in": "query", "schema": {"type": "string"}},
    "ToolFilter": {"name": "tool", "in": "query", "schema": ref("Tool")},
    "IdempotencyKey": {
        "name": "Idempotency-Key",
        "in": "header",
        "required": False,
        "schema": {"type": "string", "minLength": 1, "maxLength": 255, "pattern": "^[\\x21-\\x7e]+$"},
    },
    "IfMatch": {"name": "If-Match", "in": "header", "required": False, "schema": {"type": "string"}},
    "IfMatchRequired": {"name": "If-Match", "in": "header", "required": True, "schema": {"type": "string"}},
}

paths = {
    P + "/capabilities": {
        "get": op("getCapabilities", "Read the API version and the optional modules", [], public=True, tags=["installation"],
                  responses={"200": {"description": "OK", **json_body(ref("Capabilities"))}}),
    },
    P + "/namespaces/{namespace}/units": {
        "parameters": [p("Namespace")],
        "get": op("listUnits", "List the units of a namespace", ["unit:read"], tags=["units"],
                  params=[p("PageSize"), p("PageToken"), p("ToolFilter")],
                  responses={"200": {"description": "OK", **json_body(ref("UnitList"))}}),
    },
    P + "/namespaces/{namespace}/units/{unit_id}": {
        "parameters": [p("Namespace"), p("UnitId")],
        "get": op("getUnit", "Read a unit", ["unit:read"], tags=["units"],
                  responses={"200": {"description": "OK", "headers": ETAG_HEADER, **json_body(ref("Unit"))}}),
    },
    P + "/namespaces/{namespace}/deleted-units/{unit_id}/restore": {
        "parameters": [p("Namespace"), p("UnitId")],
        "post": op("restoreDeletedUnit", "Restore a deleted unit", ["unit:restore"], tags=["units"], params=MUTATING,
                   responses={"200": {"description": "OK", **json_body(ref("Unit"))}}),
    },
    P + "/backups": {
        "get": op("listBackups", "List backups", ["installation:backup"], tags=["backups"],
                  params=[p("PageSize"), p("PageToken")],
                  responses={"200": {"description": "OK", **json_body(ref("BackupList"))}}),
        "post": op("createBackup", "Start a backup", ["installation:backup"], tags=["backups"], params=MUTATING,
                   body={"required": False, **json_body(ref("BackupCreate"))},
                   responses={"202": {"description": "Accepted", "headers": ETAG_HEADER, **json_body(ref("Backup"))}}),
    },
    P + "/backups/{id}": {
        "parameters": [p("Id")],
        "get": op("getBackup", "Read a backup", ["installation:backup"], tags=["backups"],
                  responses={"200": {"description": "OK", "headers": ETAG_HEADER, **json_body(ref("Backup"))}}),
        "delete": op("deleteBackup", "Delete a backup", ["installation:backup"], tags=["backups"],
                     params=MUTATING + [p("IfMatchRequired")],
                     responses={"204": {"description": "Deleted"}}),
    },
    P + "/backups/{id}/check": {
        "parameters": [p("Id")],
        "post": op("checkBackup", "Check a backup", ["installation:backup"], tags=["backups"], params=MUTATING,
                   responses={"200": {"description": "OK", **json_body(ref("BackupCheck"))}}),
    },
    P + "/me/preferences": {
        "get": op("getPreferences", "Read the preferences of the caller", ["caller:read"], tags=["me"],
                  responses={"200": {"description": "OK", "headers": ETAG_HEADER, **json_body(ref("Preferences"))}}),
        "patch": op("updatePreferences", "Change the preferences of the caller", ["caller:manage-preferences"], tags=["me"],
                    params=MUTATING + [p("IfMatch")],
                    body={"required": True, **json_body(ref("Preferences"), "application/merge-patch+json")},
                    responses={"200": {"description": "OK", "headers": ETAG_HEADER, **json_body(ref("Preferences"))}}),
    },
    P + "/namespaces/{namespace}/change-orders/{id}/verdict": {
        "parameters": [p("Namespace"), p("Id")],
        "post": op("setChangeOrderVerdict", "Approve or reject a change order", ["changeorder:decide"], tags=["change-orders"],
                   params=MUTATING, body={"required": True, **json_body(ref("Verdict"))},
                   responses={"200": {"description": "OK", **json_body(ref("ChangeOrder"))}}),
    },
}

doc30 = {
    "openapi": "3.0.3",
    "info": {"title": "OpenDustpan API (trial fixture)", "version": "0.1.0", "license": {"name": "Apache-2.0"}},
    "servers": [{"url": "http://localhost:8080"}],
    "security": [{"bearerAuth": []}],
    "tags": [{"name": t} for t in ["installation", "units", "backups", "me", "change-orders"]],
    "paths": paths,
    "components": {
        "schemas": schemas,
        "parameters": parameters,
        "headers": {"ETag": {"schema": {"type": "string"}}},
        "responses": {"Error": {"description": "An error", **json_body(ref("Error"))}},
        "securitySchemes": {"bearerAuth": {"type": "http", "scheme": "bearer"}},
    },
}


def to31(node):
    """Convert the 3.0 dialect to 3.1: nullable -> a type list or oneOf null."""
    if isinstance(node, list):
        return [to31(n) for n in node]
    if not isinstance(node, dict):
        return node
    node = {k: to31(v) for k, v in node.items()}
    if node.pop("nullable", False):
        if "type" in node:
            node["type"] = [node["type"], "null"]
        else:
            # allOf wrapper of a $ref: a 3.1 document writes oneOf [ref, null].
            inner = node.pop("allOf")
            node["oneOf"] = inner + [{"type": "null"}]
    if "example" in node and "properties" not in node:
        node["examples"] = [node.pop("example")]
    return node


doc31 = to31(copy.deepcopy(doc30))
doc31["openapi"] = "3.1.0"
doc31["info"]["license"]["identifier"] = "Apache-2.0"

out = sys.argv[1]
for name, d in (("openapi-3.0.json", doc30), ("openapi-3.1.json", doc31)):
    with open(f"{out}/{name}", "w") as f:
        json.dump(d, f, indent=2, sort_keys=True)
        f.write("\n")
