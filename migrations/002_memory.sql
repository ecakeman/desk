CREATE TABLE IF NOT EXISTS memory_docs (
	run_id TEXT NOT NULL,
	seq INT NOT NULL,
	kind TEXT NOT NULL,
	text TEXT NOT NULL,
	tsv tsvector,
	PRIMARY KEY (run_id, seq)
);

CREATE INDEX IF NOT EXISTS memory_docs_tsv ON memory_docs USING gin(tsv);