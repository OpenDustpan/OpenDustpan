#!/bin/sh
# Lints the Helm chart in deploy/helm/opendustpan. It is not part of
# `make check`, so a contributor needs no Helm.
#
# - `helm lint --strict` with the default values must fail, because the
#   schema needs a database and a key-encryption key. The error must name
#   the setting.
# - `helm lint --strict` with each file in ci/ must pass.
# - `helm lint --strict` with each file in ci/refused/ must fail. Each file
#   has a line `# want: <text>`, and the output of helm must hold the text.
# - `helm template` with each file in ci/ goes through `kubeconform -strict`
#   for each Kubernetes version in KUBE_VERSIONS: the oldest and the newest
#   minor version that the Kubernetes project supports. The ServiceMonitor
#   kind is not in the core schemas, so kubeconform skips it and fetches no
#   schema for it.
set -eu

chart=deploy/helm/opendustpan
kube_versions=${KUBE_VERSIONS:-"1.35.0 1.37.0"}

missing=0
if ! command -v helm >/dev/null 2>&1; then
  echo "helm is not installed. Install it: https://helm.sh/docs/intro/install/" >&2
  missing=1
fi
if ! command -v kubeconform >/dev/null 2>&1; then
  echo "kubeconform is not installed. Install it: https://github.com/yannh/kubeconform#installation" >&2
  missing=1
fi
if [ "$missing" -ne 0 ]; then
  echo "Check the SHA-256 of each download against the checksum that the release publishes." >&2
  exit 1
fi

fail=0
# The oldest version in kube_versions satisfies the kubeVersion of Chart.yaml.
lint_version=${kube_versions%% *}

if out=$(helm lint --strict --kube-version "$lint_version" "$chart" 2>&1); then
  echo "FAIL: helm lint passed with the default values, but it must fail" >&2
  fail=1
elif ! printf '%s\n' "$out" | grep -qF -- "/database/existingSecret/name"; then
  echo "FAIL: helm lint with the default values did not name /database/existingSecret/name" >&2
  fail=1
fi

for f in "$chart"/ci/*.yaml; do
  if ! helm lint --strict --kube-version "$lint_version" "$chart" -f "$f" >/dev/null; then
    echo "FAIL: helm lint refused $f" >&2
    fail=1
  fi
done

for f in "$chart"/ci/refused/*.yaml; do
  want=$(sed -n 's/^# want: //p' "$f" | head -n 1)
  if [ -z "$want" ]; then
    echo "FAIL: $f has no '# want: <error text>' line" >&2
    fail=1
    continue
  fi
  if out=$(helm lint --strict --kube-version "$lint_version" "$chart" -f "$f" 2>&1); then
    echo "FAIL: helm lint passed $f, but it must fail" >&2
    fail=1
  elif ! printf '%s\n' "$out" | grep -qF -- "$want"; then
    echo "FAIL: helm lint of $f did not print: $want" >&2
    fail=1
  fi
done

for v in $kube_versions; do
  for f in "$chart"/ci/*.yaml; do
    if ! helm template release "$chart" -f "$f" --namespace dustpan --kube-version "$v" |
      kubeconform -strict -summary -skip ServiceMonitor -kubernetes-version "$v" >/dev/null; then
      echo "FAIL: kubeconform refused the output for $f on Kubernetes $v" >&2
      fail=1
    fi
  done
done

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "chart-lint: ok"
