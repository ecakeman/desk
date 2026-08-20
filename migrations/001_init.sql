CREATE TABLE IF NOT EXISTS sessions(
	id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS runs(
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id),
	status TEXT NOT NULL,
	workspace_dir TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS events(
	id BIGSERIAL PRIMARY KEY,
	run_id TEXT NOT NULL REFERENCES runs(id),
	seq INT NOT NULL,
	type TEXT NOT NULL,
	payload JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(run_id, seq)
);

CREATE INDEX IF NOT EXISTS events_run_id_seq_idx ON events(run_id, seq);