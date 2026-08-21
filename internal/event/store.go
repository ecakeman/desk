package event

import (
	"context"
	"database/sql"
	"encoding/json"
)

type Store struct{
	DB *sql.DB
}

func NewStore(db *sql.DB)*Store{
	return &Store{DB:db}
}

func (s *Store) Append(ctx context.Context,tx *sql.Tx,runID,typ string,payload any)error{
	raw,err:=json.Marshal(payload)
	if err!=nil{
		return err
	}
	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE run_id=$1`,
		runID,
	).Scan(&seq); err!=nil{
		return err
	}
	_,err=tx.ExecContext(ctx,
		`INSERT INTO events (run_id,seq,type,payload) VALUES ($1,$2,$3,$4)`,
		runID,seq,typ,raw,
	)
	return err
}
