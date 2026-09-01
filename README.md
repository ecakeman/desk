# Desk

Desk 是一个本地优先的 Agent 控制面。

Go 负责 Run、事件、审批和状态。Python 负责模型调用。PostgreSQL 保存持久状态，并用 pgvector 支撑 memory。Workspace 工具让 Agent 检查和修改本地文件。React 提供 Dashboard。

**Desk 把 Agent 执行当成可观察的事件驱动运行时，而不是一次模型 API 调用。**

## 架构

```text
CLI / Dashboard
       │
     HTTP/SSE
       │
       ▼
┌───────────────┐
│ Go Control    │
│ Plane         │
└───────┬───────┘
        │
 ┌──────┼──────────────┐
 ▼      ▼              ▼
DB    Worker         Plugins
       │              │
       ▼              ▼
   Flash / Pro     Workspace
```

- **Go**：session / run / event / approval，以及 `plan` → `act` → `review` 编排。
- **Python**：OpenAI 兼容的 chat completions 与 tool-call 协议。
- **PostgreSQL + pgvector**：持久事件、task，以及 memory 索引。
- **Plugins**：在 jail 限定的工作区上做文件系统与搜索；memory / task 作为宿主持有的工具。
- **React / Vite**：Dashboard 走同一套 HTTP/SSE。CLI（`desk chat` / `desk show`）是客户端，不是第二套运行时。

以上都在一台机器上运行。

## 能力

| 方面   | Desk 提供的内容                    |
| ------ | ---------------------------------- |
| 运行时 | plan → act → review                |
| 模型   | Flash / Pro 槽位                   |
| 工具   | filesystem、search、memory、tasks  |
| Memory | PostgreSQL + pgvector              |
| 安全   | 写操作需要审批                     |
| 可观察 | events + SSE + Dashboard           |
| Prompt | 版本化 catalog + 稳定 runtime 前缀 |
| 测试   | 隔离的测试数据库                   |

plan / review 走 Pro 槽，act 走 Flash。Prefix cache 按槽位隔离。单个 Run 最多进入 2 次 Pro review；超出后改走 act。

## 快速开始

**推荐：** Dev Container（Go 1.25、Python 3、Node 22、Docker-outside-of-Docker）。宿主机只需 Docker 和支持 Dev Containers 的编辑器。

否则需要：

```text
Docker / Docker Compose v2
Go 1.25
Python 3
Node.js 22.12+
```

```bash
cp .env.example .env
# 填写 DESK_MODEL_*，或 DESK_FLASH_* + DESK_PRO_*

make db-up
make db-migrate
make build
make serve
```

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
```

Dashboard：[http://127.0.0.1:8080](http://127.0.0.1:8080)

CLI（需先启动服务）：

```bash
./bin/desk chat
./bin/desk chat <session_id>
./bin/desk show <session_id>
```

聊天中 `/quit` 退出，`/stop` 取消最近一次 Run；写操作等待 `y/n`。

## 配置

从 `.env.example` 复制为 `.env`。不要提交 `.env`。Desk 通过 OpenAI 兼容 HTTP 接口调用模型，不为不同供应商增加额外适配层。

### 核心

```text
DESK_HTTP_ADDR      # 默认 :8080
DESK_WORKSPACE      # 插件 jail 根目录；默认 .
DESK_DATABASE_URL   # Postgres DSN（库名 desk）
```

### Prompt / Web

```text
DESK_PROMPTS_DIR    # 默认 prompts
DESK_WEB_DIR        # 默认 web/dist
```

每个 Run 开始时会对 catalog 做快照（稳定 system 前缀、phase / task / skill / memory 的 runtime 上下文、稳定工具顺序）。改 `prompts/` 会影响下一个 Run，无需重启进程。

### 默认模型

```text
DESK_MODEL_BASE_URL
DESK_MODEL_API_KEY
DESK_MODEL_MODEL
```

`BASE_URL` 使用对应第三方提供的兼容 chat completions 地址。

### 模型槽位

```text
DESK_FLASH_BASE_URL
DESK_FLASH_API_KEY
DESK_FLASH_MODEL

DESK_PRO_BASE_URL
DESK_PRO_API_KEY
DESK_PRO_MODEL
```

Flash / Pro 某字段为空时，回退到 `DESK_MODEL_*`。

### Embedding / Rerank

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

embedding / rerank 为可选。未配置或 rerank 失败时，Search 退回 BM25 / RRF。

## 部署

Desk 的定位是**本地单机运行**，不是托管式多租户服务。

### 本地 / Dev Container

推荐开发方式：在 Dev Container 中打开仓库，再执行「快速开始」中的命令。

### Docker / PostgreSQL

`make db-up` 会构建并启动项目所需的 PostgreSQL + pgvector。初始化脚本同时创建 Go 测试用的 `desk_test`。`make db-down` 停止该栈。

### 接近发布的本地构建

```bash
make web
make build
make serve
```

若存在 `web/dist`，Go 服务会直接托管它。若需要前端热更新，可在 `desk serve` 已运行时执行 `npm --prefix web run dev`（端口 5173），Vite 会把 `/v1` 代理到 `127.0.0.1:8080`。

### 测试库

集成测试使用独立的 `desk_test` 数据库，绝不回落到生产用的 `DESK_DATABASE_URL`。

## 开发

```bash
make fmt
make vet
make test
make test-integration
make web-lint
make web-test
make web
```

Go 集成测试跑在隔离的测试库上；真实模型 / embedding 调用不进入 CI。

可选浏览器 E2E：`make web-e2e`（Docker 中的 Playwright）。

## Showcase

```text
1 session · 4 consecutive runs · 1 small workspace
```

在未告知 Agent 应调用哪些工具、何时使用 memory / task、何时 review、以及不手工指定 phase 的前提下，连续维护 Bookmark Manager 规格（`bookmark-lab/`）：

```text
项目基线
  ↓
增量产品变更
  ↓
历史决策回顾
  ↓
review / 收口
```

| 信号                    | 结果 |
| ----------------------- | ---: |
| Flash 热前缀 cache      | ~93% |
| 单 Run 最大 Pro review  |    2 |
| Memory                  | 按需 |
| Skill 修订              |    0 |

Flash 与 Pro 使用各自独立的 cache 槽。

最终结论：`PASS WITH OBSERVATIONS`

这次 Showcase 暴露了文档维护时偏重工具调用的情况，但没有生命周期失败，也没有失控的 review 循环。

## 项目状态

```text
V1 Pro
Closed loop: 8/8
Product: 6/6
Observation: 2/2
```

V1 Pro 已冻结为本地单机 Agent 控制面。

## 范围

非目标：

- 多租户 SaaS
- 公有云部署
- HA / 水平扩展
- 分布式 Worker
- 操作系统级沙箱
- 在 CI 中调用真实模型
