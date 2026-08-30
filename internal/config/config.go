package config

import "os"

type ModelConfig struct{
	BaseURL string
	APIKey string
	Model string
}

type Config struct{
	HTTPAddr      string
	Workerspace   string
	DatabaseURL   string
	MigrationsDir string
	PluginsDir    string
	Python        string
    Agent         string
	Model         ModelConfig
	Flash         ModelConfig
	Pro           ModelConfig
	
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
		Flash: modelSlot("DESK_FLASH", ModelConfig{
			BaseURL: getenv("DESK_MODEL_BASE_URL", ""),
			APIKey:  getenv("DESK_MODEL_API_KEY", ""),
			Model:   getenv("DESK_MODEL_MODEL", ""),
		}),
		Pro: modelSlot("DESK_PRO", ModelConfig{
			BaseURL: getenv("DESK_MODEL_BASE_URL", ""),
			APIKey:  getenv("DESK_MODEL_API_KEY", ""),
			Model:   getenv("DESK_MODEL_MODEL", ""),
		}),
	}
}

func modelSlot(prefix string, fallback ModelConfig) ModelConfig {
	return ModelConfig{
		BaseURL: getenv(prefix+"_BASE_URL", fallback.BaseURL),
		APIKey:  getenv(prefix+"_API_KEY", fallback.APIKey),
		Model:   getenv(prefix+"_MODEL", fallback.Model),
	}
}

func getenv(key, fallback string) string {
	if v:=os.Getenv(key); v!=""{
		return v
	}
	return fallback
}