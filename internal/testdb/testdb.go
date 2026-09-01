// Package testdb 只给 Go 集成测试用：连独立库 desk_test，不碰正式 desk。
package testdb

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"desk/internal/db"
	"desk/internal/ids"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DefaultURL 是测试默认 DSN；正式 serve 仍用 config 里的 desk。
const DefaultURL = "postgres://desk:desk@127.0.0.1:5432/desk_test?sslmode=disable"

var (
	ensureOnce sync.Once
	ensureErr  error
)

// URL 只认 DESK_TEST_DATABASE_URL，绝不回落到 DESK_DATABASE_URL。
func URL() string {
	if v := strings.TrimSpace(os.Getenv("DESK_TEST_DATABASE_URL")); v != "" {
		return v
	}
	return DefaultURL
}

// MigrationsDir 指向仓库 migrations/。
func MigrationsDir() string {
	if mig := os.Getenv("DESK_MIGRATION_DIR"); mig != "" {
		return mig
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
}

// Open 确保 desk_test 存在、跑 migration，失败则 Skip。连接在 t.Cleanup 关闭。
func Open(t *testing.T) *sql.DB {
	t.Helper()
	ensure(t)
	if ensureErr != nil {
		t.Skip(ensureErr)
	}
	ctx := context.Background()
	sqlDB, err := db.Connect(ctx, URL())
	if err != nil {
		t.Skip(err)
	}
	if err := db.Migrate(ctx, sqlDB, MigrationsDir()); err != nil {
		_ = sqlDB.Close()
		t.Skip(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

// InsertSession 插入一条 open session，并按 id 注册 Cleanup。
func InsertSession(t *testing.T, sqlDB *sql.DB) string {
	t.Helper()
	id := ids.New()
	if _, err := sqlDB.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, id); err != nil {
		t.Fatal(err)
	}
	CleanupSession(t, sqlDB, id)
	return id
}

// CleanupSession 按 session_id 删 memory_docs → events → runs → sessions。
func CleanupSession(t *testing.T, sqlDB *sql.DB, sessionID string) {
	t.Helper()
	if sessionID == "" {
		return
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = sqlDB.ExecContext(ctx, `
			DELETE FROM memory_docs
			 WHERE run_id IN (SELECT id FROM runs WHERE session_id=$1)`, sessionID)
		_, _ = sqlDB.ExecContext(ctx, `
			DELETE FROM events
			 WHERE run_id IN (SELECT id FROM runs WHERE session_id=$1)`, sessionID)
		_, _ = sqlDB.ExecContext(ctx, `DELETE FROM runs WHERE session_id=$1`, sessionID)
		_, _ = sqlDB.ExecContext(ctx, `DELETE FROM sessions WHERE id=$1`, sessionID)
	})
}

// CleanupMemory 只删指定 run_id 的 memory_docs（无 session 的索引测例）。
func CleanupMemory(t *testing.T, sqlDB *sql.DB, runIDs ...string) {
	t.Helper()
	copied := append([]string(nil), runIDs...)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range copied {
			if id == "" {
				continue
			}
			_, _ = sqlDB.ExecContext(ctx, `DELETE FROM memory_docs WHERE run_id=$1`, id)
		}
	})
}

func ensure(t *testing.T) {
	t.Helper()
	ensureOnce.Do(func() {
		testURL := URL()
		name, err := dbName(testURL)
		if err != nil {
			ensureErr = err
			return
		}
		admin, err := sql.Open("pgx", withDB(testURL, "postgres"))
		if err != nil {
			ensureErr = err
			return
		}
		defer admin.Close()
		if err := admin.Ping(); err != nil {
			ensureErr = err
			return
		}
		var exists bool
		if err := admin.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, name,
		).Scan(&exists); err != nil {
			ensureErr = err
			return
		}
		if exists {
			return
		}
		_, ensureErr = admin.Exec(`CREATE DATABASE ` + pqIdent(name) + ` OWNER desk`)
	})
}

func dbName(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(u.Path, "/"), nil
}

func withDB(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + name
	return u.String()
}

func pqIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
