package db

import (
	"context"
	"database/sql"
	"fmt"
)

// EnsureEmbeddingColumn 确保 pgvector 扩展和 memory_docs.embedding 维度与配置一致。
func EnsureEmbeddingColumn(ctx context.Context, db *sql.DB, dim int) error {
	if dim <= 0 || dim > 8192 {
		return fmt.Errorf("bad_embed_dim: %d", dim)
	}
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return err
	}
	var typmod sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT a.atttypmod
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		WHERE c.relname = 'memory_docs' AND a.attname = 'embedding' AND a.attnum > 0 AND NOT a.attisdropped`,
	).Scan(&typmod)
	if err == sql.ErrNoRows {
		_, err = db.ExecContext(ctx, fmt.Sprintf(
			`ALTER TABLE memory_docs ADD COLUMN embedding vector(%d)`, dim,
		))
		return err
	}
	if err != nil {
		return err
	}
	if !typmod.Valid {
		return fmt.Errorf("memory_docs.embedding missing typmod")
	}
	// pgvector 把维度存在 atttypmod 本身，不是 varchar 那种 typmod+4
	got := int(typmod.Int64)
	if got != dim {
		return fmt.Errorf("memory_docs.embedding dim=%d want=%d; drop column or set DESK_EMBEDDING_DIM", got, dim)
	}
	return nil
}
