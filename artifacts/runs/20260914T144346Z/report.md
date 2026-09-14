# Test Report

## 1. Environment

- Repository: `desk`
- Commit: `1983e8b336559d61c935f12516951fbb08123bf5`
- Timestamp: `2026-09-14T14:43:46.174635+00:00`
- Go: `go version go1.26.6 linux/amd64`
- Command: `python3 scripts/collect_verify.py`
- Smoke: `False`

## 2. Commands

- `make db-up db-migrate`
- `go test -json -p 1 -count=1 -timeout 180s ./cmd/... ./internal/... ./plugins/...`
- `go test -json -run '^TestRuntimeContractLifecycle$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractToolLifecycle$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractToolFailure$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractApprovalReject$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractApprovalAllow$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractCancel$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractEventConsistency$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractModelRouting$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractReviewBudget$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractPromptSnapshot$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractMemoryFallback$' ./internal/memory/`
- `go test -json -run '^TestRuntimeContractShowcase$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractHTTPLifecycle$' ./internal/httpapi/`
- `go test -json -run '^TestRuntimeContractTurnFinishSchema$' ./internal/run/`
- `go test -json -run '^TestRuntimeContractHTTPApprovalReject$' ./internal/httpapi/`
- `go test -json -run '^TestRuntimeContractHTTPCancel$' ./internal/httpapi/`
- `go test -json -run 'TestEvictedBuffer|TestInvariant|...' ./internal/ctxmgr/`

## 3. Test Scope

Deterministic Go tests and (optional) live showcase are **separate metrics**.
Runtime Contracts N=13 is the verify.sh contract list only.

## 4. Raw Results

- Run directory: `artifacts/runs/20260914T144346Z`
- Normalized records: 165

## 5. Metrics

| Metric | Value | N | Source | Formula |
|---|---:|---:|---|---|
| runtime_contract_pass_rate | 13/13 | 13 | `raw/contracts/*.jsonl` | passed_named_contracts / 13 (catalog runtime_contracts); smoke uses N=ran |
| go_integration_pass_rate | 135/135 | 136 | `raw/integration.jsonl` | top_level Test pass / (pass+fail); skips excluded from denominator |
| context_invariant_pass_rate | 13/13 | 13 | `raw/ctxmgr.jsonl` | ctxmgr invariant/evicted/compact tests pass / total ran |
| worker_protocol_extra_pass_rate | 3/3 | 3 | `raw/extra/*.jsonl` | TurnFinishSchema + HTTP approval/cancel pass / N; NOT part of 13/13 |
| live_showcase_success_rate | NOT_RUN | 0 | `(absent)` | completed_runs / 4 from scripts/showcase_live.py artifact |

## 6. Failures / Exceptions

None in this run.

## 7. Reproduction

```text
make db-up db-migrate
python3 scripts/collect_verify.py
```

## 8. Conclusion

Numbers in this file are computed from `metrics.json` / `normalized.json` of this run.
See `TEST_DATA.md` for lineage.
