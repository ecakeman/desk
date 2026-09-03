# Desk

**本地优先的 Agent 工作台与执行运行时。**

Desk 将 Agent 执行抽象为一个**可持久化、可观察、可中断、带人工审批的事件驱动 Runtime**，而不是一次模型 API 调用。

Go 负责 Runtime 控制面与状态管理，Python 负责模型执行，PostgreSQL 保存事件与持久状态，pgvector 支撑 Memory 检索，Workspace Plugins 负责受控文件操作。

```text
CLI / Dashboard
        │
      HTTP / SSE
        │
        ▼
┌─────────────────────┐
│    Go Control Plane │
│                     │
│ Session / Run /     │
│ Event / Approval    │
└──────────┬──────────┘
           │
     ┌─────┴─────┐
     │           │
     ▼           ▼
ContextManager  Worker
     │           │
     │        Python Process
     │           │
     │        Flash / Pro
     │
     ▼
PostgreSQL + pgvector
     │
     ▼
Workspace / Memory / Task
```



## Core Design



### Event-driven Runtime

Desk 以 Event Store 作为执行事实源：

```text
User Message
    ↓
Run Created
    ↓
Plan
    ↓
Act
    ↓
Tool Request
    ↓
Approval
    ↓
Tool Execution
    ↓
Review
    ↓
Run Completed / Interrupted
```

Run 不直接持有隐式状态，关键生命周期变化都通过 Event 持久化。

这使 Runtime 可以：

- 观察完整执行轨迹
- 在进程退出后识别 orphan Run
- 对 Tool / Approval / Interrupt 做确定性状态控制
- 从事件重新构建运行上下文



### ContextManager

ContextManager 负责**一次 LLM Call 应该看到什么**。

```text
Event Store
     ↓
ContextManager
 ├── Long-term Large Compact
 ├── Small Compact
 ├── Structured Facts
 ├── Recent Window
 ├── Skill
 └── Retrieval
     ↓
Context Assembly
     ↓
Worker
     ↓
LLM
```

上下文采用分层策略：

```text
Stable Prefix
────────────────────────────
Large
Small
Facts
Recent Window
────────────────────────────
Dynamic Suffix
Skill
Retrieval
Runtime
```

窗口淘汰通过 durable `context.evicted` 记录，历史内容进入 Small Compact；多个 Small 再滚动合并为新的 Large baseline。

ContextManager 同时按照**真实 LLM 请求预算**进行规划：

```text
System
+ Tools
+ Messages
+ Runtime
≤ Total Budget
```

Tools 按实际发送的 OpenAI-shaped schema 计入预算；在预算不足时优先移除 Retrieval / Skill / Facts，不任意截断长期状态。

没有可用 Compactor 时不会产生新的 durable eviction，避免原始历史在无法压缩时被不可逆丢弃。

### Tool Safety

模型只能提出 Tool Request，实际副作用由 Go Runtime 执行。

```text
LLM
 ↓
tool.request
 ↓
Risk Decision
 ├── allow
 └── waiting_approval
         ↓
      human decision
         ↓
    Plugin Execute
         ↓
    tool.completed
```

高风险写操作进入 Human-in-the-loop Approval。

Workspace 操作通过 Plugin Registry 执行，而不是让 Python Runtime 直接修改宿主环境。

### Memory

Memory 使用 PostgreSQL + pgvector。

当前检索链路支持：

```text
BM25 / PostgreSQL FTS
        +
Vector Search
        ↓
       RRF
        ↓
      Rerank
```

Embedding / Rerank 均为可选；未配置或失败时保持 BM25 / RRF fallback。

Memory 是从 Event Store 派生的索引，不承担 Runtime 真相。

### Model Routing

Desk 使用 Flash / Pro 双模型槽位：

```text
plan   → Pro
review → Pro
act    → Flash
```

单个 Run 的 Pro Review 次数受预算控制，避免在普通执行路径中重复消耗高成本模型。

Prompt 使用版本化 Catalog，并通过 Prompt Hash 记录本次 Run 使用的稳定 Prompt 快照。

## Runtime Guarantees

当前 Runtime 契约覆盖：

```text
Run lifecycle
Tool Calling
Approval
Cancellation / Interrupt
Event consistency
Model routing
Prompt snapshot
Memory fallback
Multi-run continuity
Context budget
Context compaction
Worker adoption
```

核心约束：

```text
Event Store = source of truth
ContextManager = context decision layer
Worker = model execution layer
Memory = derived retrieval index
Recover = interrupt orphan runs
```

ContextManager 不接管 Worker 生命周期，也不把 Memory / Prompt Catalog / Event Store 重新抽象成第二套真相源。

## Showcase

真实模型连续运行验证：

```text
Session             1
Consecutive Runs    4
Runtime Contracts   13 / 13
Tool Execution      PASS
Human Approval      PASS
Memory Continuity   PASS
Task Continuity     PASS
Review Budget       PASS
Event Consistency   PASS
```

在同一个 Session 中连续完成 4 个 Run，验证 Workspace Mutation、Tool Calling、Human-in-the-loop Approval、Memory / Task Continuity 与 Pro Review。

Flash 热前缀 Cache 命中约：

```text
93%
```



## Quick Start

推荐使用 Dev Container。

环境要求：

```text
Docker / Docker Compose v2
Go 1.25
Python 3
Node.js 22.12+
```

配置：

```bash
cp .env.example .env
```

填写模型配置后：

```bash
make db-up
make db-migrate
make build
make serve
```

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
```

Dashboard：

```text
http://127.0.0.1:8080
```

CLI：

```bash
./bin/desk chat
./bin/desk chat <session_id>
./bin/desk show <session_id>
```

模型通过 OpenAI-compatible Chat Completions HTTP 接口访问。Desk 不针对不同供应商增加额外 Provider Adapter。

## Configuration



### Core

```text
DESK_HTTP_ADDR
DESK_WORKSPACE
DESK_DATABASE_URL
```



### Model

```text
DESK_MODEL_BASE_URL
DESK_MODEL_API_KEY
DESK_MODEL_MODEL
```

或者分别配置：

```text
DESK_FLASH_BASE_URL
DESK_FLASH_API_KEY
DESK_FLASH_MODEL

DESK_PRO_BASE_URL
DESK_PRO_API_KEY
DESK_PRO_MODEL
```

未配置的 Flash / Pro 字段回退到 `DESK_MODEL_*`。

### Memory

```text
DESK_EMBEDDING_BASE_URL
DESK_EMBEDDING_API_KEY
DESK_EMBEDDING_MODEL
DESK_EMBEDDING_DIM

DESK_RERANK_BASE_URL
DESK_RERANK_API_KEY
DESK_RERANK_MODEL
DESK_RERANK_TIMEOUT_MS
```



### Context

```text
DESK_CTX_WINDOW_TOKENS
DESK_CTX_TOTAL_TOKENS
DESK_CTX_SMALL_TRIGGER_TOKENS
DESK_CTX_LARGE_TRIGGER_TOKENS
DESK_CTX_LARGE_SMALL_COUNT
DESK_CTX_RETRIEVAL_K
```

`DESK_CTX_TOTAL_TOKENS` 是一次实际 LLM 请求的硬预算上限。

## Development

```bash
make fmt
make vet
make test
make test-integration
make test-runtime
make verify
make web-lint
make web-test
make web
```

集成测试使用独立 `desk_test` 数据库。

Runtime Contract Verification：

```bash
make verify
```

真实模型 Showcase：

```bash
make showcase-live
```

重置 Showcase Workspace：

```bash
make showcase-reset
```

默认 CI 不调用真实模型。

## Project Structure

```text
cmd/
├── desk/                 CLI entry

internal/
├── run/                  Run lifecycle / orchestration
├── ctxmgr/               Context planning / compaction
├── event/                Event Store
├── worker/               Worker protocol
├── prompt/               Prompt catalog / snapshot
├── memory/               Memory index / retrieval
├── plugin/               Plugin registry
├── approve/              Approval policy
└── httpapi/              HTTP / SSE API

agent/
└── worker.py             Python model runtime

web/
└──                     React / Vite Dashboard

deployments/
└──                     PostgreSQL / local deployment

fixtures/
└── bookmark-lab/         Showcase workspace
```



## Scope

Desk V1 定位为**本地单机 Agent Runtime**。

明确非目标：

- 多租户 SaaS
- 公有云部署
- HA / 水平扩展
- 分布式 Worker
- 操作系统级沙箱
- CI 中调用真实模型



