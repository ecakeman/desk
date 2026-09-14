# Test Data

每个写入 README / 面试的数字必须能沿这条链反查。

- commit: `1983e8b336559d61c935f12516951fbb08123bf5`
- run: `artifacts/runs/20260914T144346Z`

## Runtime Contracts (13)

公式：`passed / 13`，分母固定为 catalog `runtime_contracts`，不是全部 go test。

- `TestRuntimeContractLifecycle` — Run Lifecycle — `internal/run/lifecycle_runtime_test.go`
- `TestRuntimeContractToolLifecycle` — Tool Calling — `internal/run/lifecycle_runtime_test.go`
- `TestRuntimeContractToolFailure` — Tool Calling — `internal/run/lifecycle_runtime_test.go`
- `TestRuntimeContractApprovalReject` — Approval — `internal/run/lifecycle_runtime_test.go`
- `TestRuntimeContractApprovalAllow` — Approval — `internal/run/lifecycle_runtime_test.go`
- `TestRuntimeContractCancel` — Cancel / Interrupt — `internal/run/lifecycle_runtime_test.go`
- `TestRuntimeContractEventConsistency` — Event Consistency — `internal/run/lifecycle_runtime_test.go`
- `TestRuntimeContractModelRouting` — Model Routing — `internal/run/model_prompt_runtime_test.go`
- `TestRuntimeContractReviewBudget` — Context Budget — `internal/run/model_prompt_runtime_test.go`
- `TestRuntimeContractPromptSnapshot` — Prompt Snapshot — `internal/run/model_prompt_runtime_test.go`
- `TestRuntimeContractMemoryFallback` — Memory Fallback — `internal/memory/search_runtime_test.go`
- `TestRuntimeContractShowcase` — Showcase — `internal/run/showcase_runtime_test.go`
- `TestRuntimeContractHTTPLifecycle` — Run Lifecycle — `internal/httpapi/runtime_contract_test.go`

## Metrics lineage

### runtime_contract_pass_rate

- value: `13/13`
- N: `13`
- formula: `passed_named_contracts / 13 (catalog runtime_contracts); smoke uses N=ran`
- source: `raw/contracts/*.jsonl`
- cases:
  - `TestRuntimeContractLifecycle`
  - `TestRuntimeContractToolLifecycle`
  - `TestRuntimeContractToolFailure`
  - `TestRuntimeContractApprovalReject`
  - `TestRuntimeContractApprovalAllow`
  - `TestRuntimeContractCancel`
  - `TestRuntimeContractEventConsistency`
  - `TestRuntimeContractModelRouting`
  - `TestRuntimeContractReviewBudget`
  - `TestRuntimeContractPromptSnapshot`
  - `TestRuntimeContractMemoryFallback`
  - `TestRuntimeContractShowcase`
  - `TestRuntimeContractHTTPLifecycle`

### go_integration_pass_rate

- value: `135/135`
- N: `136`
- formula: `top_level Test pass / (pass+fail); skips excluded from denominator`
- source: `raw/integration.jsonl`

### context_invariant_pass_rate

- value: `13/13`
- N: `13`
- formula: `ctxmgr invariant/evicted/compact tests pass / total ran`
- source: `raw/ctxmgr.jsonl`
- cases:
  - `TestInvariantFinalEstimateLeqTotal`
  - `TestInvariantEvictedNeverResurrects`
  - `TestEvictedBufferVisibleBeforeSmallCompact`
  - `TestEvictedBufferRespectsIndependentCap`
  - `TestInvariantSmallFailNoRetryUntilNewEvict`
  - `TestInvariantActiveLargeUniqueAndSmallsAfter`
  - `TestInvariantFactProvenanceAllowed`
  - `TestInvariantNormalCallPrefixStable`
  - `TestInvariantReconstructSkipsCompactLLM`
  - `TestEvictedBufferRefLineageAndSmallAbsorb`
  - `TestPrepareSmallCompactSuccessAndFailure`
  - `TestLargeRollingBaseline`
  - `TestTotalBudgetBoundsEstimate`

### worker_protocol_extra_pass_rate

- value: `3/3`
- N: `3`
- formula: `TurnFinishSchema + HTTP approval/cancel pass / N; NOT part of 13/13`
- source: `raw/extra/*.jsonl`
- cases:
  - `TestRuntimeContractTurnFinishSchema`
  - `TestRuntimeContractHTTPApprovalReject`
  - `TestRuntimeContractHTTPCancel`

### live_showcase_success_rate

- value: `NOT_RUN`
- N: `0`
- formula: `completed_runs / 4 from scripts/showcase_live.py artifact`
- source: `(absent)`
