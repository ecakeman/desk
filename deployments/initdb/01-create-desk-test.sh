#!/bin/bash
set -euo pipefail
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
SELECT 'CREATE DATABASE desk_test OWNER desk'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'desk_test')\gexec
EOSQL
