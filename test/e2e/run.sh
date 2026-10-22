#!/usr/bin/env bash
# End-to-end test against a real cluster (created by kind in CI).
set -euo pipefail

bin=./bin/kube-upgrade-preflight
current=$(kubectl version -o json | jq -r '.serverVersion.minor' | tr -dc '0-9')
target="v1.$((current + 1))"
echo "control plane minor: 1.${current}, simulating upgrade to ${target}"

echo "::group::clean cluster must not FAIL"
$bin --target "$target" --output markdown | tee -a "${GITHUB_STEP_SUMMARY:-/dev/null}"
echo "::endgroup::"

kubectl apply -f test/e2e/blocking.yaml
kubectl -n preflight-e2e rollout status deploy/web --timeout=120s
kubectl -n preflight-e2e wait --for=condition=Ready pod/debug --timeout=120s
# Give the disruption controller time to compute the PDB status.
for _ in $(seq 30); do
  [ "$(kubectl -n preflight-e2e get pdb web -o jsonpath='{.status.expectedPods}')" = "1" ] && break
  sleep 2
done

echo "::group::blocking workloads must FAIL"
set +e
out=$($bin --target "$target" --server-dry-run --namespace preflight-e2e --output markdown)
code=$?
set -e
echo "$out" | tee -a "${GITHUB_STEP_SUMMARY:-/dev/null}"
echo "::endgroup::"

fail() { echo "e2e: $*" >&2; exit 1; }
[ "$code" -eq 1 ] || fail "expected exit code 1, got $code"
grep -q "pdb/preflight-e2e/web" <<<"$out" || fail "PDB check did not report the blocking budget"
grep -q "pod preflight-e2e/debug has no controller" <<<"$out" || fail "unmanaged pod was not reported"
grep -q "API server rejected dry-run eviction of preflight-e2e/web-" <<<"$out" || fail "server dry-run eviction was not rejected"
echo "e2e: all expectations met"
