package event

import (
	"context"
	"database/sql"
	"encoding/json"
)

type Store struct {
	DB       *sql.DB
	OnInsert func(ctx context.Context, runID string, seq int, typ string, payload json.RawMessage) error
}

func NewStore(db *sql.DB)*Store{
	return &Store{DB:db}
}

type Event struct{
	RunID   string          `json:"run_id,omitempty"`
	Seq     int             `json:"seq"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func (s *Store) Append(ctx context.Context,tx *sql.Tx,runID,typ string,payload any)(int,error){
	raw,err:=json.Marshal(payload)
	if err!=nil{
		return 0,err
	}
	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE run_id=$1`,
		runID,
	).Scan(&seq); err!=nil{
		return 0,err
	}
	_,err=tx.ExecContext(ctx,
		`INSERT INTO events (run_id,seq,type,payload) VALUES ($1,$2,$3,$4)`,
		runID,seq,typ,raw,
	)
	if err != nil {
		return seq, err
	}
	if s.OnInsert != nil {
		_ = s.OnInsert(ctx, runID, seq, typ, raw)
	}
	return seq,nil
}

func (s *Store) Get(ctx context.Context, runID string, seq int) (Event, error) {
	var e Event
	err := s.DB.QueryRowContext(ctx,
		`SELECT seq,type,payload FROM events WHERE run_id=$1 AND seq=$2`,
		runID, seq,
	).Scan(&e.Seq, &e.Type, &e.Payload)
	return e, err
}

func (s *Store) ListAfter(ctx context.Context,runID string,after int)([]Event,error){
	rows,err:=s.DB.QueryContext(ctx,
	`SELECT seq,type,payload FROM events WHERE run_id=$1 AND seq>$2 ORDER BY seq`,
	runID,after,
	)
	if err!=nil{
		return nil,err
	}
	defer rows.Close()
	var out []Event
	for rows.Next(){
		var e Event
		if err:=rows.Scan(&e.Seq,&e.Type,&e.Payload);err!=nil{
			return nil,err
		}
		out=append(out,e)
	}
	return out,rows.Err()
}

func (s *Store) ListBySession(ctx context.Context, sessionID string) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT e.run_id, e.seq, e.type, e.payload
		FROM events e
		JOIN runs r ON r.id = e.run_id
		WHERE r.session_id = $1
		ORDER BY r.created_at, e.seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.RunID, &e.Seq, &e.Type, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}