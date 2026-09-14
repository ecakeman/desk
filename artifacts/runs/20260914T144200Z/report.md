# Test Report

## 1. Environment

- Repository: `desk`
- Commit: `1983e8b336559d61c935f12516951fbb08123bf5`
- Timestamp: `2026-09-14T14:42:00.538185+00:00`
- Go: `go version go1.26.6 linux/amd64`
- Command: `python3 scripts/collect_verify.py --smoke`
- Smoke: `True`

## 2. Commands

- `go test -json -run '^TestRuntimeContractLifecycle$' ./internal/run/`

## 3. Test Scope

Deterministic Go tests and (optional) live showcase are **separate metrics**.
Runtime Contracts N=13 is the verify.sh contract list only.

## 4. Raw Results

- Run directory: `artifacts/runs/20260914T144200Z`
- Normalized records: 1

## 5. Metrics

| Metric | Value | N | Source | Formula |
|---|---:|---:|---|---|
| runtime_contract_pass_rate | 1/1 | 1 | `raw/contracts/*.jsonl` | passed_named_contracts / 13 (catalog runtime_contracts); smoke uses N=ran |
| go_integration_pass_rate | NOT_RUN | 0 | `raw/integration.jsonl` | top_level Test pass / (pass+fail); skips excluded from denominator |
| context_invariant_pass_rate | NOT_RUN | 0 | `raw/ctxmgr.jsonl` | ctxmgr invariant/evicted/compact tests pass / total ran |
| worker_protocol_extra_pass_rate | NOT_RUN | 0 | `raw/extra/*.jsonl` | TurnFinishSchema + HTTP approval/cancel pass / N; NOT part of 13/13 |
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
