#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
BASE="${DESK_SHOWCASE_URL:-http://127.0.0.1:8080}"
STARTED=0
if ! curl -sf "${BASE}/healthz" >/dev/null; then
  if [[ ! -x "$ROOT/bin/desk" ]]; then
    echo "missing bin/desk; run make go-build plugins" >&2
    exit 1
  fi
  echo "starting desk serve DESK_WORKSPACE=$ROOT/ws-probe"
  DESK_WORKSPACE="$ROOT/ws-probe" "$ROOT/bin/desk" serve &
  STARTED=$!
  trap 'if [[ "${STARTED:-0}" -gt 0 ]]; then kill "$STARTED" 2>/dev/null || true; fi' EXIT
  ok=0
  for _ in $(seq 1 40); do
    if curl -sf "${BASE}/healthz" >/dev/null; then
      ok=1
      break
    fi
    sleep 0.25
  done
  if [[ "$ok" -ne 1 ]]; then
    echo "desk serve failed to become healthy" >&2
    exit 1
  fi
fi

exec python3 "$ROOT/scripts/showcase_live.py" "$@"
