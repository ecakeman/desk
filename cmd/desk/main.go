// Command desk 是单机 Agent 控制面入口：serve 组装依赖并监听 HTTP，
// chat / show 只作为同一套 /v1 的客户端，不直连数据库或模型。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"desk/internal/cli"
	"desk/internal/config"
	"desk/internal/ctxmgr"
	"desk/internal/db"
	"desk/internal/event"
	"desk/internal/httpapi"
	"desk/internal/memory"
	"desk/internal/plugin"
	"desk/internal/prompt"
	"desk/internal/run"
	"desk/internal/session"
	"desk/internal/task"
	"desk/internal/worker"
)

func main() {
	cfg := config.Load()
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "serve":
		if err := runServe(cfg); err != nil {
			log.Fatal(err)
		}
	case "chat":
		sid := ""
		if len(os.Args) > 2 {
			sid = os.Args[2]
		}
		if err := cli.Chat(cli.New(cfg.HTTPAddr), sid); err != nil {
			log.Fatal(err)
		}
	case "show":
		if len(os.Args) < 3 {
			log.Fatal("usage: desk show <session_id>")
		}
		if err := cli.Show(cli.New(cfg.HTTPAddr), os.Args[2]); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unknown command: %s", cmd)
	}
}

// runServe 接通 Postgres、事件索引、插件与 Worker，再把非终态 Run 标成 interrupted。
func runServe(cfg config.Config) error {
	ctx := context.Background()
	if _, err := prompt.Load(cfg.PromptsDir); err != nil {
		return err
	}
	sqlDB, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB, cfg.MigrationsDir); err != nil {
		return err
	}
	if cfg.EmbedOK() {
		if err := db.EnsureEmbeddingColumn(ctx, sqlDB, cfg.EmbedDim); err != nil {
			return err
		}
	}

	ev := event.NewStore(sqlDB)
	idx := memory.New(sqlDB)
	idx.OnError = func(err error) {
		log.Printf("memory index: %v", err)
	}
	if cfg.EmbedOK() {
		idx.Embedder = memory.NewHTTPEmbedder(cfg.Embed.BaseURL, cfg.Embed.APIKey, cfg.Embed.Model, cfg.EmbedDim)
		idx.Dim = cfg.EmbedDim
	}
	if cfg.RerankOK() {
		timeout := time.Duration(cfg.RerankTimeoutMS) * time.Millisecond
		idx.Reranker = memory.NewHTTPReranker(cfg.Rerank.BaseURL, cfg.Rerank.APIKey, cfg.Rerank.Model, timeout)
		idx.RerankTimeout = timeout
	}
	ev.OnInsert = idx.IndexTx
	if err := idx.Sync(ctx); err != nil {
		return err
	}
	reg, err := plugin.Load(cfg.PluginsDir, cfg.Workspace)
	if err != nil {
		return err
	}
	reg.Put(memory.NewHost(idx))
	reg.Put(task.NewHost(sqlDB, ev))
	svc := run.NewService(sqlDB, ev)
	svc.Plugins = reg
	svc.Index = idx
	svc.Flash = cfg.Flash
	svc.Pro = cfg.Pro
	svc.PromptsDir = cfg.PromptsDir
	svc.Context = ctxmgr.New(ev, idx, ctxmgr.Settings{
		WindowTokens:    cfg.WindowTokens,
		SmallTriggerTok: cfg.SmallTriggerTok,
		LargeTriggerTok: cfg.LargeTriggerTok,
		LargeSmallCount: cfg.LargeSmallCount,
		RetrievalK:      cfg.RetrievalK,
		PromptsDir:      cfg.PromptsDir,
	})
	if cfg.CompactOK() {
		svc.Context.Compactor = ctxmgr.NewHTTPCompactor(cfg.Compact.BaseURL, cfg.Compact.APIKey, cfg.Compact.Model)
	}
	svc.Worker = worker.NewProcess(cfg.Python, cfg.Agent, append(os.Environ(),
		"DESK_MODEL_BASE_URL="+cfg.Model.BaseURL,
		"DESK_MODEL_API_KEY="+cfg.Model.APIKey,
		"DESK_MODEL_MODEL="+cfg.Model.Model,
	))
	if err := svc.Recover(ctx); err != nil {
		return err
	}

	mux := httpapi.NewMux(httpapi.Deps{
		DB:        sqlDB,
		Workspace: cfg.Workspace,
		WebDir:    cfg.WebDir,
		Sessions:  session.NewStore(sqlDB),
		Runs:      run.NewStore(sqlDB),
		Messages:  svc,
		Events:    ev,
	})
	log.Printf("desk serve %s", cfg.HTTPAddr)
	return http.ListenAndServe(cfg.HTTPAddr, mux)
}
