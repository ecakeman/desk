package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"desk/internal/cli"
	"desk/internal/config"
	"desk/internal/db"
	"desk/internal/event"
	"desk/internal/httpapi"
	"desk/internal/memory"
	"desk/internal/plugin"
	"desk/internal/run"
	"desk/internal/session"
	"desk/internal/task"
	"desk/internal/worker"
)

func main(){
	cfg := config.Load()
	cmd := "serve"
	if len(os.Args) > 1{
		cmd = os.Args[1]
	}

	switch cmd {
	case "serve":
		if err :=runServe(cfg);err!=nil {
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

func runServe(cfg config.Config) error {
	ctx := context.Background()
	sqlDB, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB, cfg.MigrationsDir); err != nil {
		return err
	}

	ev := event.NewStore(sqlDB)
	idx := memory.New(sqlDB)
	ev.OnInsert = idx.Index
	if err := idx.Rebuild(ctx); err != nil {
		return err
	}
	reg, err := plugin.Load(cfg.PluginsDir, cfg.Workerspace)
	if err != nil {
		return err
	}
	reg.Put(memory.NewHost(idx))
	reg.Put(task.NewHost(sqlDB, ev))
	svc := run.NewService(sqlDB, ev)
	svc.Plugins = reg
	svc.Flash = cfg.Flash
	svc.Pro = cfg.Pro
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
		Workspace: cfg.Workerspace,
		Sessions:  session.NewStore(sqlDB),
		Runs:      run.NewStore(sqlDB),
		Messages:  svc,
		Events:    ev,
	})
	log.Printf("desk serve %s", cfg.HTTPAddr)
	return http.ListenAndServe(cfg.HTTPAddr, mux)
}