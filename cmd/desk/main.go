package main

import(
	"context"
	"log"
	"net/http"
	"os"

	"desk/internal/httpapi"
	"desk/internal/db"
	"desk/internal/config"
	"desk/internal/event"
	"desk/internal/session"
	"desk/internal/run"
	"desk/internal/plugin"
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
	case "chat","show":
		log.Fatal("no")
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
 	reg, err := plugin.Load(cfg.PluginsDir, cfg.Workerspace)
 	if err != nil {
		return err
	}
 	svc := run.NewService(sqlDB, ev)
 	svc.Plugins = reg
 	svc.Worker = worker.Fake{}
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