# Desk

Desk 是一个面向本地单机的 Agent 控制面：Go 服务负责会话、Run、审批、事件流和记忆，
Python 3 标准库 Worker 调用 OpenAI 兼容模型，React/Vite Dashboard 展示运行状态，
Postgres + pgvector 保存事实与向量索引。

## 架构

```text
CLI (desk chat/show) ─┐
                      ├─ HTTP/SSE ─> Gin API ─> session/run/event/memory ─> Postgres + pgvector
React Dashboard ──────┘                    │
                                          ├─ subprocess ─> Python Worker ─> 模型 API
                                          └─ subprocess ─> fs/search 插件 ─> 本地工作区
```

所有组件都运行在一台开发机上。Go 服务在启动时执行 `migrations/*.sql`，提供 `/v1`
API、SSE 事件流，并在 `web/dist` 存在时托管 Dashboard。Worker 不依赖第三方 Python
包；插件是由 Go 编译出的独立进程。

## 前置条件

推荐使用 Dev Container，只需宿主机提供：

- Docker Engine 或 Docker Desktop（包含 Compose v2）
- 支持 Dev Containers 的编辑器

直接在宿主机运行时需要：

- Go 1.25
- Python 3
- Node.js 22.12+ 和 npm
- Docker Compose v2

仓库提交的是 `web/package-lock.json`，因此可复现构建使用 `npm ci`。Dev Container
同时安装 pnpm，供交互开发使用，但不要在没有同步锁文件的情况下用 pnpm 替代 CI
安装流程。

### 国内镜像

`.devcontainer/Dockerfile` 默认使用阿里云 Debian apt、npmmirror Node/npm 和
`goproxy.cn`。打开 Dev Container 前可在宿主环境覆盖：

```bash
export DESK_APT_MIRROR=https://your-mirror.example
export DESK_NODE_MIRROR=https://your-mirror.example/node
export DESK_NPM_REGISTRY=https://your-mirror.example/npm/
export DESK_GOPROXY=https://your-mirror.example/go/,direct
```

Postgres 开发镜像沿用 `deployments/Dockerfile.postgres` 中已有的镜像源。GitHub
Actions 使用公开官方镜像，不依赖本地镜像配置。

## 配置

从模板生成本地配置：

```bash
cp .env.example .env
```

常用变量：

- `DESK_HTTP_ADDR`：监听地址，默认 `:8080`
- `DESK_WORKSPACE`：插件可访问的本地工作区
- `DESK_DATABASE_URL`：Postgres DSN
- `DESK_PROMPTS_DIR`、`DESK_WEB_DIR`：Prompt 和静态 Dashboard 目录
- `DESK_MODEL_*`：默认模型；`BASE_URL` 应是完整的 OpenAI 兼容 chat completions 地址
- `DESK_FLASH_*`、`DESK_PRO_*`：可选模型档位，未设置时回退到默认模型
- `DESK_EMBEDDING_*`：可选 embedding 地址、模型和维度；地址可写 API 根路径或
  `/embeddings` 完整地址
- `DESK_RERANK_*`：可选 rerank 地址、模型和超时；未配置或调用失败时 Search 退回 RRF/BM25 顺序

`.env` 只用于本机，API Key 不应写进 README、命令行、Git 提交或 CI。模板中的 Key
保持为空；真实模型和 embedding 只在本地配置。

## 构建与运行

```bash
make db-up
make db-migrate
make build
make serve
```

`make db-up` 构建并启动本地 pgvector Postgres；`make db-migrate` 显式应用 SQL。
`make serve` 会重新构建后在 `http://127.0.0.1:8080` 启动 Desk，服务启动时也会
幂等执行迁移。检查：

```bash
curl http://127.0.0.1:8080/healthz
```

停止数据库：

```bash
make db-down
```

## CLI

服务运行后，在另一个终端启动新会话：

```bash
./bin/desk chat
```

继续已有会话或查看事件：

```bash
./bin/desk chat <session_id>
./bin/desk show <session_id>
```

交互中 `/quit` 退出，`/stop` 取消最近一次 Run；写操作会出现 `y/n` 审批。

## Dashboard

生产式本地构建由 Go 服务直接托管：

```bash
make web
make serve
# http://127.0.0.1:8080
```

前端热更新模式需要 Go 服务已运行：

```bash
npm --prefix web ci
npm --prefix web run dev
# http://127.0.0.1:5173
```

Vite 会把 `/v1` 代理到 `127.0.0.1:8080`。

## Prompt 目录

`prompts/` 是运行时 Prompt catalog：

- `system/base.md`：所有阶段共享的系统约束
- `phases/{plan,act,review}.md`：阶段说明
- `tools/*.md`：各工具暴露给模型的描述

这些文件在服务启动时先做完整性校验；每个 Run 开始时固定一份快照并计算 hash。
修改文案会影响下一个 Run，无需重启服务，正在执行的 Run 不漂移。Prompt 不是
Python Worker 内的硬编码模板。

## 测试与检查

```bash
make fmt             # 格式化 Go
make fmt-check       # 只检查，不改文件
make vet             # Go 静态检查
make test            # Go 测试；数据库不可用时相关测试按现有逻辑跳过
make test-integration # 启动/迁移 Postgres，并以 -p 1 串行执行全部 Go 测试
make web-lint
make web-test
make web             # npm ci + typecheck/Vite build
docker run --rm --ipc=host --user "$(id -u):$(id -g)" -e HOME=/tmp \
  -v "$PWD/web:/work" -w /work \
  mcr.microsoft.com/playwright:v1.62.1-noble npm run e2e
```

测试层级：

1. Go 单元测试：纯状态、Prompt、jail、投影等，不要求外部服务。
2. Go/Postgres 集成测试：事务、事件序列、HTTP 路由和记忆索引；必须 `-p 1`
   避免共享数据库中的并发互扰。
3. Web：ESLint、Vitest、TypeScript 和 Vite build。
4. 真实模型/embedding：仅本地人工验证，不进入 CI，不向 CI 注入供应商密钥。

GitHub Actions 分开执行无数据库的 Go 单元阶段、pgvector Postgres 集成阶段和 Web
阶段。所有 Go 包枚举都由 `go` 工具完成，不扫描 `web/node_modules`。

## 非目标

- 不是多租户 SaaS，不提供账号、鉴权、配额或租户隔离。
- 不提供公网 TLS、云部署、水平扩展、HA、备份或灾备。
- 插件路径检查和超时不是操作系统级安全沙箱，不应运行不受信任代码。
- 不在 CI 调用真实模型或 embedding，也不管理生产密钥。
- 不保证跨主机分布式 Worker、消息队列或远程插件协议。
