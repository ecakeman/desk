package memory

import (
	"os"
	"path/filepath"
	"runtime"
)

func migrationDir() string {
	if mig := os.Getenv("DESK_MIGRATION_DIR"); mig != "" {
		return mig
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
}
