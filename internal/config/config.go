// Package config 从环境和 .env 装载进程配置；只在启动时读，不入业务表。
package config

import (
	"os"
	"strings"
)

// ModelConfig 是一个模型槽位：聊天、embedding、rerank 共用同一组字段。
type ModelConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Config 是 desk serve 的全部进程配置。
type Config struct {
	HTTPAddr        string
	Workspace       string
	DatabaseURL     string
	MigrationsDir   string
	PluginsDir      string
	PromptsDir      string
	WebDir          string
	Python          string
	Agent           string
	Model           ModelConfig
	Flash           ModelConfig
	Pro             ModelConfig
	Embed           ModelConfig
	EmbedDim        int
	Rerank          ModelConfig
	RerankTimeoutMS int
	Compact         ModelConfig
	WindowTokens    int
	SmallTriggerTok int
	LargeTriggerTok int
	LargeSmallCount int
	RetrievalK      int
}

// Load 先读 .env 再读环境变量；已存在的环境变量不被 .env 覆盖。
func Load() Config {
	loadDotEnv(".env")
	c := Config{
		HTTPAddr:      getenv("DESK_HTTP_ADDR", ":8080"),
		Workspace:     getenv("DESK_WORKSPACE", getenv("DESK_WORKERSAPCE", ".")),
		DatabaseURL:   getenv("DESK_DATABASE_URL", "postgres://desk:desk@localhost:5432/desk?sslmode=disable"),
		MigrationsDir: getenv("DESK_MIGRATION_DIR", "migrations"),
		PluginsDir:    getenv("DESK_PLUGINS_DIR", "plugins"),
		PromptsDir:    getenv("DESK_PROMPTS_DIR", "prompts"),
		WebDir:        getenv("DESK_WEB_DIR", "web/dist"),
		Python:        getenv("DESK_PYTHON", "python3"),
		Agent:         getenv("DESK_AGENT", "agent/worker.py"),
		Model: ModelConfig{
			BaseURL: getenv("DESK_MODEL_BASE_URL", ""),
			APIKey:  getenv("DESK_MODEL_API_KEY", ""),
			Model:   getenv("DESK_MODEL_MODEL", ""),
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
		Embed: ModelConfig{
			BaseURL: getenv("DESK_EMBEDDING_BASE_URL", ""),
			APIKey:  getenv("DESK_EMBEDDING_API_KEY", ""),
			Model:   getenv("DESK_EMBEDDING_MODEL", ""),
		},
		EmbedDim: getenvInt("DESK_EMBEDDING_DIM", 0),
		Rerank: ModelConfig{
			BaseURL: getenv("DESK_RERANK_BASE_URL", ""),
			APIKey:  getenv("DESK_RERANK_API_KEY", ""),
			Model:   getenv("DESK_RERANK_MODEL", ""),
		},
		RerankTimeoutMS: getenvInt("DESK_RERANK_TIMEOUT_MS", 3000),
		Compact: modelSlot("DESK_COMPACT", ModelConfig{
			BaseURL: getenv("DESK_FLASH_BASE_URL", getenv("DESK_MODEL_BASE_URL", "")),
			APIKey:  getenv("DESK_FLASH_API_KEY", getenv("DESK_MODEL_API_KEY", "")),
			Model:   getenv("DESK_FLASH_MODEL", getenv("DESK_MODEL_MODEL", "")),
		}),
		WindowTokens:    getenvInt("DESK_CTX_WINDOW_TOKENS", 4000),
		SmallTriggerTok: getenvInt("DESK_CTX_SMALL_TRIGGER_TOKENS", 400),
		LargeTriggerTok: getenvInt("DESK_CTX_LARGE_TRIGGER_TOKENS", 1200),
		LargeSmallCount: getenvInt("DESK_CTX_LARGE_SMALL_COUNT", 3),
		RetrievalK:      getenvInt("DESK_CTX_RETRIEVAL_K", 8),
	}
	return c
}

// CompactOK 表示 compact 槽有 URL 和模型，才走独立压缩 LLM。
func (c Config) CompactOK() bool {
	return c.Compact.BaseURL != "" && c.Compact.Model != ""
}

// EmbedOK 表示 embedding 三件套（URL、模型、维度）都齐，才挂 HTTPEmbedder。
func (c Config) EmbedOK() bool {
	return c.Embed.BaseURL != "" && c.Embed.Model != "" && c.EmbedDim > 0
}

// RerankOK 表示 rerank 的 URL 和模型都齐；未配则 Search 停在 BM25/RRF。
func (c Config) RerankOK() bool {
	return c.Rerank.BaseURL != "" && c.Rerank.Model != ""
}

func modelSlot(prefix string, fallback ModelConfig) ModelConfig {
	return ModelConfig{
		BaseURL: getenv(prefix+"_BASE_URL", fallback.BaseURL),
		APIKey:  getenv(prefix+"_API_KEY", fallback.APIKey),
		Model:   getenv(prefix+"_MODEL", fallback.Model),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func loadDotEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		k = strings.TrimPrefix(k, "export ")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if n := len(v); n >= 2 {
			if q := v[0]; (q == '"' || q == '\'') && v[n-1] == q {
				v = v[1 : n-1]
			}
		}
		if v == "" {
			continue
		}
		_ = os.Setenv(k, v)
	}
}
