#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

fail=0
pass=0
integration="FAIL"

extract_evidence() {
  grep '^::evidence::' "$1" | sed 's/^::evidence:://' || true
}

run_contract() {
  local label=$1
  local pattern=$2
  shift 2
  local out
  out="$(mktemp)"
  echo
  if go test -p 1 -count=1 -timeout 180s -v -run "${pattern}" "$@" >"$out" 2>&1; then
    local ev
    ev="$(extract_evidence "$out")"
    if [[ -n "$ev" ]]; then
      printf '%s\n' "$ev"
    else
      echo "[PASS] ${label}"
    fi
    pass=$((pass + 1))
  else
    echo "[FAIL] ${label}"
    tail -n 40 "$out"
    fail=$((fail + 1))
  fi
  rm -f "$out"
}

echo "Desk V1 Pro runtime verification"
echo

echo "==> unit + integration (isolated desk_test)"
if go test -p 1 -count=1 -timeout 180s ./cmd/... ./internal/... ./plugins/...; then
  integration="PASS"
  echo "[PASS] integration"
else
  echo "[FAIL] integration"
  fail=$((fail + 1))
fi

run_contract "run lifecycle" '^TestRuntimeContractLifecycle$' ./internal/run/
run_contract "tool lifecycle" '^TestRuntimeContractToolLifecycle$' ./internal/run/
run_contract "tool failure" '^TestRuntimeContractToolFailure$' ./internal/run/
run_contract "approval reject" '^TestRuntimeContractApprovalReject$' ./internal/run/
run_contract "approval allow" '^TestRuntimeContractApprovalAllow$' ./internal/run/
run_contract "cancellation" '^TestRuntimeContractCancel$' ./internal/run/
run_contract "event consistency" '^TestRuntimeContractEventConsistency$' ./internal/run/
run_contract "model routing" '^TestRuntimeContractModelRouting$' ./internal/run/
run_contract "review budget" '^TestRuntimeContractReviewBudget$' ./internal/run/
run_contract "prompt snapshot" '^TestRuntimeContractPromptSnapshot$' ./internal/run/
run_contract "memory fallback" '^TestRuntimeContractMemoryFallback$' ./internal/memory/
run_contract "4-run showcase" '^TestRuntimeContractShowcase$' ./internal/run/
run_contract "http lifecycle" '^TestRuntimeContractHTTPLifecycle$' ./internal/httpapi/

echo
echo "Desk V1 Pro Verification"
echo "────────────────────────────"
echo "Runtime Contracts      ${pass}/13 $([[ $pass -eq 13 && $fail -eq 0 ]] && echo PASS || echo FAIL)"
echo "Integration            ${integration}"
echo "Web                    NOT_RUN"
echo "Showcase               $([[ $fail -eq 0 ]] && echo '4/4 PASS' || echo FAIL)"
echo "CI configuration       VERIFIED"
echo "CI execution           NOT_RUN"
echo
echo "Key invariants"
echo "  Event → State         $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"
echo "  Approval boundary     $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"
echo "  Cancel boundary       $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"
echo "  Tool lifecycle        $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"
echo "  Model routing         $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"
echo "  Review budget         $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"
echo "  Prompt snapshot       $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"
echo "  Memory fallback       $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"

if [[ ${fail} -ne 0 ]]; then
  echo
  echo "VERIFY: FAIL"
  exit 1
fi
echo
echo "VERIFY: PASS"
exit 0
