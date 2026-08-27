package config

import "os"

type ModelConfig struct{
	BaseURL string
	APIKey string
	Model string
}

type Config struct{
	HTTPAddr string
	Workerspace string
	DatabaseURL string
	MigrationsDir string
	PluginsDir string
	Python string
    Agent  string
	Model ModelConfig
	
}

func Load() Config{
	return Config{
		HTTPAddr: getenv("DESK_HTTP_ADDR", ":8080"),
		Workerspace: getenv("DESK_WORKERSAPCE", "."),
		DatabaseURL: getenv("DESK_DATABASE_URL", "postgres://desk:desk@localhost:5432/desk?sslmode=disable"),
		MigrationsDir: getenv("DESK_MIGRATION_DIR", "migrations"),
		PluginsDir: getenv("DESK_PLUGINS_DIR", "plugins"),
		Python: getenv("DESK_PYTHON", "python3"),
		Agent:  getenv("DESK_AGENT", "agent/worker.py"),
		Model: ModelConfig{
			BaseURL: getenv("DESK_MODEL_BASE_URL", ""),
			APIKey: getenv("DESK_MODEL_API_KEY", ""),
			Model: getenv("DESK_MODEL_MODEL", ""),
		},
	}
}

func getenv(key, fallback string) string {
	if v:=os.Getenv(key); v!=""{
		return v
	}
	return fallback
}