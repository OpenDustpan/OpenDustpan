#!/bin/sh
# Replay OpenTofu state versions from one TFE-compatible API to another (T2).
#
# Support claim: this script replays state through a TFE-compatible API with
# OpenDustpan as the source. It does not cover HCP Terraform as the source.
# The replay keeps the serial, the lineage and the MD5 of each version. The
# original kind, time and user of a version are lost: the destination records
# a new version at the time of the replay. A replayed version carries no
# outputs document until the next apply writes one; `state pull` still shows
# the outputs in the state.
#
# The script reads the last N state versions of the source workspace through
# the list route, downloads each version, and creates it on the destination
# from the oldest to the newest: lock, create the state version, upload the
# state (the upload finalizes the version), unlock. It resumes: a version
# that the destination already holds with the same serial, lineage and MD5
# is not created again, so a second run moves nothing. It never writes to the
# source: every request to the source is a GET. It records the newest source
# version before it writes and stops when the source gains a new state
# version during the run. With --dry-run it only reads and changes nothing.
# It never prints a token: the tokens travel in curl config files, never in
# arguments or output.
#
# Usage:
#   IMPORT_TFE_SOURCE_TOKEN=<token> IMPORT_TFE_DEST_TOKEN=<token> \
#     scripts/import-tfe-state.sh \
#       --source-url https://source.example.com \
#       --dest-url https://dest.example.com \
#       --source-org acme --source-workspace web \
#       [--dest-org acme] [--dest-workspace web] \
#       [--count 10] [--dry-run] [--ca-bundle bundle.pem] [--insecure]
#
# Environment:
#   IMPORT_TFE_SOURCE_TOKEN  token for the source API (required)
#   IMPORT_TFE_DEST_TOKEN    token for the destination API (required)
#   IMPORT_TFE_CA_BUNDLE     PEM bundle file for both servers (or --ca-bundle)
#
# Options:
#   --source-url URL        base URL of the source API (required)
#   --dest-url URL          base URL of the destination API (required)
#   --source-org ORG        namespace of the source workspace (required)
#   --source-workspace WS   name of the source workspace (required)
#   --dest-org ORG          namespace of the destination workspace
#                           (default: the source organization)
#   --dest-workspace WS     name of the destination workspace
#                           (default: the source workspace name)
#   --count N, -n N         how many of the newest source versions to replay
#                           (default: 10)
#   --dry-run               list what the replay would move and change nothing
#   --ca-bundle FILE        PEM bundle file that both servers trust
#   --insecure              skip TLS verification (test servers only)
#   --help, -h              show this text and exit
#
# Needs curl, jq and md5sum on PATH.
set -eu

PROG="import-tfe-state"
COUNT_DEFAULT=10

src_url=""
dst_url=""
src_org=""
src_ws=""
dst_org=""
dst_ws=""
count="$COUNT_DEFAULT"
dry_run=0
ca_bundle="${IMPORT_TFE_CA_BUNDLE:-}"
insecure=0

usage() {
  sed -n '2,/^# Needs/p' "$0" | sed 's/^# \{0,1\}//'
}

die() {
  echo "$PROG: $1" >&2
  exit "${2:-1}"
}

need_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    die "missing required tool: $1"
  fi
}

urlencode() {
  jq -rn --arg s "$1" '$s|@uri'
}

# curl_auth writes a curl config file holding the bearer token, so the token
# never shows in arguments. Tokens hold no quote or newline; refuse others.
curl_auth() {
  file="$1"
  token="$2"
  case "$token" in
    *'"'* | *'\'* | *'
'*)
      die "token is empty or holds a quote, a backslash or a newline"
      ;;
  esac
  printf 'header = "Authorization: Bearer %s"\n' "$token" >"$file"
  chmod 600 "$file"
}

# curl_base prints the shared curl options: quiet, TLS trust and failures.
curl_base() {
  printf '%s' "-sS"
  if [ -n "$ca_bundle" ]; then
    printf ' %s' "--cacert $ca_bundle"
  fi
  if [ "$insecure" -eq 1 ]; then
    printf ' %s' "--insecure"
  fi
}

# api_get writes the body of GET url to outfile and prints the HTTP status.
# Only GET ever runs against the source.
api_get() {
  cfg="$1"
  url="$2"
  outfile="$3"
  # shellcheck disable=SC2086
  curl $(curl_base) -K "$cfg" -o "$outfile" -w '%{http_code}' "$url"
}

# api_post writes the body of POST url with a JSON body to outfile and prints
# the HTTP status. It only runs against the destination.
api_post() {
  cfg="$1"
  url="$2"
  body="$3"
  outfile="$4"
  # shellcheck disable=SC2086
  curl $(curl_base) -K "$cfg" -o "$outfile" -w '%{http_code}' \
    -X POST -H 'Content-Type: application/vnd.api+json' -d "$body" "$url"
}

# api_put uploads file to url with no credentials: the upload URL holds the
# capability. It prints the HTTP status.
api_put() {
  url="$1"
  file="$2"
  # shellcheck disable=SC2086
  curl $(curl_base) -o /dev/null -w '%{http_code}' \
    -X PUT -H 'Content-Type: application/octet-stream' --data-binary "@$file" "$url"
}

# workspace_id prints the workspace ID for org/name on base, or "" (with
# status on stdout) when the workspace is missing.
workspace_id() {
  cfg="$1"
  base="$2"
  org="$3"
  name="$4"
  out="$WORK/status.json"
  code="$(api_get "$cfg" "$base/api/v2/organizations/$(urlencode "$org")/workspaces/$(urlencode "$name")" "$out")"
  if [ "$code" = "404" ]; then
    printf '%s' "missing:$code"
    return 0
  fi
  if [ "$code" != "200" ]; then
    die "read workspace $org/$name answers $code: $(cat "$out")"
  fi
  jq -r '.data.id // empty' "$out"
}

md5_of() {
  if command -v md5sum >/dev/null 2>&1; then
    md5sum "$1" | cut -d' ' -f1
  elif command -v md5 >/dev/null 2>&1; then
    md5 -r "$1" | cut -d' ' -f1
  else
    openssl dgst -md5 -r "$1" | cut -d' ' -f1
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --source-url)
      src_url="${2:?missing value for $1}"; shift 2 ;;
    --dest-url)
      dst_url="${2:?missing value for $1}"; shift 2 ;;
    --source-org)
      src_org="${2:?missing value for $1}"; shift 2 ;;
    --source-workspace)
      src_ws="${2:?missing value for $1}"; shift 2 ;;
    --dest-org)
      dst_org="${2:?missing value for $1}"; shift 2 ;;
    --dest-workspace)
      dst_ws="${2:?missing value for $1}"; shift 2 ;;
    --count | -n)
      count="${2:?missing value for $1}"; shift 2 ;;
    --dry-run)
      dry_run=1; shift ;;
    --ca-bundle)
      ca_bundle="${2:?missing value for $1}"; shift 2 ;;
    --insecure)
      insecure=1; shift ;;
    --help | -h)
      usage; exit 0 ;;
    *)
      die "unknown argument: $1 (see --help)" 2 ;;
  esac
done

[ -n "$src_url" ] || die "missing --source-url (see --help)" 2
[ -n "$dst_url" ] || die "missing --dest-url (see --help)" 2
[ -n "$src_org" ] || die "missing --source-org (see --help)" 2
[ -n "$src_ws" ] || die "missing --source-workspace (see --help)" 2
[ -z "$dst_org" ] && dst_org="$src_org"
[ -z "$dst_ws" ] && dst_ws="$src_ws"

case "$count" in
  *[!0-9]* | '' | 0)
    die "--count holds $count, want a positive number" 2 ;;
esac

need_tool curl
need_tool jq

src_token="${IMPORT_TFE_SOURCE_TOKEN:-}"
dst_token="${IMPORT_TFE_DEST_TOKEN:-}"
[ -n "$src_token" ] || die "IMPORT_TFE_SOURCE_TOKEN is not set" 2
[ -n "$dst_token" ] || die "IMPORT_TFE_DEST_TOKEN is not set" 2
if [ -n "$ca_bundle" ] && [ ! -f "$ca_bundle" ]; then
  die "ca bundle $ca_bundle is not a file" 2
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT INT TERM

curl_auth "$WORK/src.cfg" "$src_token"
curl_auth "$WORK/dst.cfg" "$dst_token"
src_token=""
dst_token=""

q_org="$(urlencode "$src_org")"
q_ws="$(urlencode "$src_ws")"
dq_org="$(urlencode "$dst_org")"
dq_ws="$(urlencode "$dst_ws")"

src_id="$(workspace_id "$WORK/src.cfg" "$src_url" "$src_org" "$src_ws")"
case "$src_id" in
  missing:*)
    die "source workspace $src_org/$src_ws is missing" ;;
  "")
    die "source workspace $src_org/$src_ws answers no id" ;;
esac

# The newest N source versions, oldest first, with id, serial, lineage, md5.
code="$(api_get "$WORK/src.cfg" \
  "$src_url/api/v2/state-versions?filter%5Borganization%5D%5Bname%5D=$q_org&filter%5Bworkspace%5D%5Bname%5D=$q_ws&page%5Bsize%5D=$count" \
  "$WORK/src.json")"
[ "$code" = "200" ] || die "list source versions answers $code: $(cat "$WORK/src.json")"
src_total="$(jq -r '.meta.pagination."total-count" // 0' "$WORK/src.json")"
jq -c '[.data[] | {id: .id, serial: .attributes.serial, lineage: .attributes.lineage, md5: .attributes.md5}] | reverse | .[]' \
  "$WORK/src.json" >"$WORK/plan.jsonl"
src_n="$(wc -l <"$WORK/plan.jsonl" | tr -d ' ')"
[ "$src_n" -gt 0 ] || die "source workspace $src_org/$src_ws holds no state version"
src_newest="$(jq -r '.data[0].id // empty' "$WORK/src.json")"

dst_id="$(workspace_id "$WORK/dst.cfg" "$dst_url" "$dst_org" "$dst_ws")"
dst_missing=0
case "$dst_id" in
  missing:*)
    dst_missing=1 ;;
  "")
    die "destination workspace $dst_org/$dst_ws answers no id" ;;
esac

# Every triple the destination holds, as serial|lineage|md5 lines.
: >"$WORK/have.txt"
if [ "$dst_missing" -eq 0 ]; then
  page=1
  while :; do
    code="$(api_get "$WORK/dst.cfg" \
      "$dst_url/api/v2/state-versions?filter%5Borganization%5D%5Bname%5D=$dq_org&filter%5Bworkspace%5D%5Bname%5D=$dq_ws&page%5Bsize%5D=100&page%5Bnumber%5D=$page" \
      "$WORK/dst.json")"
    [ "$code" = "200" ] || die "list destination versions answers $code: $(cat "$WORK/dst.json")"
    got="$(jq -r '.data[] | "\(.attributes.serial)|\(.attributes.lineage)|\(.attributes.md5)"' "$WORK/dst.json")"
    [ -n "$got" ] || break
    printf '%s\n' "$got" >>"$WORK/have.txt"
    page=$((page + 1))
  done
fi

# The versions to create: source versions whose triple is missing on D.
: >"$WORK/todo.jsonl"
: >"$WORK/skip.txt"
while IFS= read -r item; do
  [ -n "$item" ] || continue
  key="$(printf '%s' "$item" | jq -r '"\(.serial)|\(.lineage)|\(.md5)"')"
  if grep -F -x -q "$key" "$WORK/have.txt"; then
    printf '%s\n' "$key" >>"$WORK/skip.txt"
  else
    printf '%s\n' "$item" >>"$WORK/todo.jsonl"
  fi
done <"$WORK/plan.jsonl"
todo_n="$(grep -c . "$WORK/todo.jsonl" || true)"
skip_n="$(grep -c . "$WORK/skip.txt" || true)"

echo "$PROG: source holds $src_total versions, replaying $src_n, destination already holds $skip_n, moving $todo_n"

if [ "$dry_run" -eq 1 ]; then
  while IFS= read -r item; do
    [ -n "$item" ] || continue
    echo "$PROG: would replay serial $(printf '%s' "$item" | jq -r '.serial') md5 $(printf '%s' "$item" | jq -r '.md5')"
  done <"$WORK/todo.jsonl"
  echo "$PROG: dry run, changed nothing"
  exit 0
fi

if [ "$dst_missing" -eq 1 ]; then
  body="$(jq -n --arg name "$dst_ws" '{data: {type: "workspaces", attributes: {name: $name}}}')"
  code="$(api_post "$WORK/dst.cfg" "$dst_url/api/v2/organizations/$dq_org/workspaces" "$body" "$WORK/created.json")"
  [ "$code" = "201" ] || die "create destination workspace answers $code: $(cat "$WORK/created.json")"
  dst_id="$(jq -r '.data.id // empty' "$WORK/created.json")"
  [ -n "$dst_id" ] || die "created destination workspace answers no id"
  echo "$PROG: created destination workspace $dst_org/$dst_ws"
fi

created=0
while IFS= read -r item; do
  [ -n "$item" ] || continue
  id="$(printf '%s' "$item" | jq -r '.id')"
  serial="$(printf '%s' "$item" | jq -r '.serial')"
  lineage="$(printf '%s' "$item" | jq -r '.lineage')"
  want_md5="$(printf '%s' "$item" | jq -r '.md5')"

  # Stop when the source gains a new version during the run.
  code="$(api_get "$WORK/src.cfg" \
    "$src_url/api/v2/state-versions?filter%5Borganization%5D%5Bname%5D=$q_org&filter%5Bworkspace%5D%5Bname%5D=$q_ws&page%5Bsize%5D=1" \
    "$WORK/guard.json")"
  [ "$code" = "200" ] || die "reread source versions answers $code: $(cat "$WORK/guard.json")"
  now_newest="$(jq -r '.data[0].id // empty' "$WORK/guard.json")"
  if [ "$now_newest" != "$src_newest" ]; then
    die "source gained a new state version during the run, stopped after $created of $todo_n"
  fi

  code="$(api_get "$WORK/src.cfg" "$src_url/api/v2/state-versions/$id/download" "$WORK/state.bin")"
  [ "$code" = "200" ] || die "download source version $id answers $code"
  got_md5="$(md5_of "$WORK/state.bin")"
  [ "$got_md5" = "$want_md5" ] || die "downloaded version $id has md5 $got_md5, want $want_md5"

  code="$(api_post "$WORK/dst.cfg" "$dst_url/api/v2/workspaces/$dst_id/actions/lock" '{"data":{}}' "$WORK/lock.json")"
  [ "$code" = "200" ] || die "lock destination workspace answers $code: $(cat "$WORK/lock.json")"

  body="$(jq -n --argjson serial "$serial" --arg lineage "$lineage" --arg md5 "$want_md5" \
    '{data: {type: "state-versions", attributes: {serial: $serial, lineage: $lineage, md5: $md5}}}')"
  code="$(api_post "$WORK/dst.cfg" "$dst_url/api/v2/workspaces/$dst_id/state-versions" "$body" "$WORK/made.json")"
  if [ "$code" != "201" ]; then
    api_post "$WORK/dst.cfg" "$dst_url/api/v2/workspaces/$dst_id/actions/unlock" '' "$WORK/unlock.json" >/dev/null
    die "create destination version serial $serial answers $code: $(cat "$WORK/made.json")"
  fi
  upload_url="$(jq -r '.data.attributes."hosted-state-upload-url" // empty' "$WORK/made.json")"
  [ -n "$upload_url" ] || {
    api_post "$WORK/dst.cfg" "$dst_url/api/v2/workspaces/$dst_id/actions/unlock" '' "$WORK/unlock.json" >/dev/null
    die "created version answers no upload url"
  }

  code="$(api_put "$upload_url" "$WORK/state.bin")"
  if [ "$code" != "200" ]; then
    api_post "$WORK/dst.cfg" "$dst_url/api/v2/workspaces/$dst_id/actions/unlock" '' "$WORK/unlock.json" >/dev/null
    die "upload serial $serial answers $code"
  fi

  code="$(api_post "$WORK/dst.cfg" "$dst_url/api/v2/workspaces/$dst_id/actions/unlock" '' "$WORK/unlock.json")"
  [ "$code" = "200" ] || die "unlock destination workspace answers $code: $(cat "$WORK/unlock.json")"

  created=$((created + 1))
  echo "$PROG: replayed serial $serial md5 $want_md5 ($created of $todo_n)"
done <"$WORK/todo.jsonl"

code="$(api_get "$WORK/src.cfg" \
  "$src_url/api/v2/state-versions?filter%5Borganization%5D%5Bname%5D=$q_org&filter%5Bworkspace%5D%5Bname%5D=$q_ws&page%5Bsize%5D=1" \
  "$WORK/guard.json")"
[ "$code" = "200" ] || die "reread source versions answers $code: $(cat "$WORK/guard.json")"
now_newest="$(jq -r '.data[0].id // empty' "$WORK/guard.json")"
[ "$now_newest" = "$src_newest" ] || die "source gained a new state version during the run"

echo "$PROG: replayed $created versions, skipped $skip_n"
