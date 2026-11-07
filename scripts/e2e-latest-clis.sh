#!/usr/bin/env bash
# Prints the newest stable release of the Pulumi CLI and of OpenTofu as two
# lines for $GITHUB_OUTPUT: "pulumi=<x.y.z>" and "tofu=<x.y.z>". CI keys the
# cache of the "latest" CLI archives on these versions, so each new release
# is downloaded once and then restored from the cache.
#
# It reads the same addresses as LatestURL of Pulumi() and OpenTofu() in
# e2e/tool.go, and picks the version the same way as the e2e fetcher.
#
# Each value goes into a cache key and a path pattern, so the script fails
# when an answer is not a plain x.y.z version.
set -euo pipefail

fetch() {
	curl --fail --silent --show-error --location --retry 3 --max-time 30 "$1"
}

pulumi=$(fetch https://www.pulumi.com/latest-version | tr -d '[:space:]')
pulumi=${pulumi#v}

tofu=$(fetch https://get.opentofu.org/tofu/api.json | jq -r '
	[.versions[].id | select(test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))]
	| max_by(split(".") | map(tonumber)) // empty')

for pair in "pulumi=$pulumi" "tofu=$tofu"; do
	if [[ ! ${pair#*=} =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		echo "error: the newest ${pair%%=*} version is not x.y.z: '${pair#*=}'" >&2
		exit 1
	fi
done

printf 'pulumi=%s\ntofu=%s\n' "$pulumi" "$tofu"
