// Package db 连接 Postgres 并按文件名顺序执行 migrations；不持有业务表语义。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Connect 打开 pgx 连接并 Ping。
func Connect(ctx context.Context, databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate 按文件名排序执行 dir 下全部 .sql。
func Migrate(ctx context.Context, db *sql.DB, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("migrations dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	for _, file := range files {
		sqlBytes, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("migrating %s: %w", file, err)
		}
	}
	return nil
}
