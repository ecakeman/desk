#!/usr/bin/env python3
"""Desk：go test 原始输出 → normalized / metrics / report。禁止手填数字。"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
CATALOG_PATH = ROOT / "artifacts" / "catalog.json"
GO_PACKAGES = ["./cmd/...", "./internal/...", "./plugins/..."]
SECRET_RE = re.compile(
    r"(?i)(sk-[a-z0-9]+|bearer\s+[a-z0-9._-]+|api[_-]?key[=:][^\s,]+)"
)


def redact(text: str) -> str:
    return SECRET_RE.sub("[REDACTED]", text)


def utc_stamp() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def run_cmd(args: list[str], *, cwd: Path, env: dict[str, str] | None = None, timeout: int = 600) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        args,
        cwd=str(cwd),
        env=env,
        text=True,
        capture_output=True,
        timeout=timeout,
    )


def git_sha() -> str:
    p = run_cmd(["git", "rev-parse", "HEAD"], cwd=ROOT, timeout=10)
    return (p.stdout or "").strip() or "unknown"


def go_version() -> str:
    p = run_cmd(["go", "version"], cwd=ROOT, timeout=10)
    return redact((p.stdout or p.stderr or "").strip())


def load_catalog() -> dict[str, Any]:
    return json.loads(CATALOG_PATH.read_text(encoding="utf-8"))


def parse_go_jsonl(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    if not path.exists():
        return rows
    for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if obj.get("Action") in {"pass", "fail", "skip"} and obj.get("Test"):
            rows.append(obj)
    return rows


def top_level_tests(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return [r for r in rows if "/" not in str(r.get("Test") or "")]


def extract_evidence(text: str) -> list[str]:
    out = []
    for line in text.splitlines():
        if line.startswith("::evidence::"):
            out.append(line[len("::evidence::") :])
        elif "::evidence::" in line:
            out.append(line.split("::evidence::", 1)[1])
    return out


def extract_measurements(text: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    chunks: list[str] = []
    for line in text.splitlines():
        payload = line
        if line.startswith("{"):
            try:
                obj = json.loads(line)
                payload = str(obj.get("Output") or "")
            except json.JSONDecodeError:
                payload = line
        if "::measurement::" in payload:
            chunks.append(payload.split("::measurement::", 1)[1].strip())
    for raw in chunks:
        try:
            parsed = json.loads(raw)
            if parsed not in out:
                out.append(parsed)
        except json.JSONDecodeError:
            out.append({"raw": raw})
    return out


def write_json(path: Path, obj: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(obj, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def sanitize_env() -> dict[str, str]:
    keep = {}
    for k, v in os.environ.items():
        lk = k.lower()
        if any(x in lk for x in ("key", "token", "secret", "password", "dsn")):
            keep[k] = "[REDACTED]"
        elif k.startswith("DESK_") or k in {"DATABASE_URL", "HOME", "USER"}:
            keep[k] = redact(v)
    return keep


def count_status(rows: list[dict[str, Any]]) -> dict[str, int]:
    c = {"passed": 0, "failed": 0, "skipped": 0}
    for r in rows:
        a = r.get("Action")
        if a == "pass":
            c["passed"] += 1
        elif a == "fail":
            c["failed"] += 1
        elif a == "skip":
            c["skipped"] += 1
    return c


def metric(
    name: str,
    value: Any,
    *,
    n: int,
    formula: str,
    source: str,
    cases: list[str] | None = None,
    unit: str = "count",
    aggregation: str = "ratio_or_count",
) -> dict[str, Any]:
    return {
        "name": name,
        "value": value,
        "sample_count": n,
        "measurement_unit": unit,
        "formula": formula,
        "source": source,
        "aggregation_method": aggregation,
        "cases": cases or [],
    }


def render_report(meta: dict, metrics: list[dict], normalized: list[dict], failures: list[dict]) -> str:
    lines = [
        "# Test Report",
        "",
        "## 1. Environment",
        "",
        f"- Repository: `{meta.get('repository')}`",
        f"- Commit: `{meta.get('commit_sha')}`",
        f"- Timestamp: `{meta.get('timestamp')}`",
        f"- Go: `{meta.get('go_version')}`",
        f"- Command: `{meta.get('command')}`",
        f"- Smoke: `{meta.get('smoke')}`",
        "",
        "## 2. Commands",
        "",
    ]
    for c in meta.get("commands", []):
        lines.append(f"- `{c}`")
    lines += [
        "",
        "## 3. Test Scope",
        "",
        "Deterministic Go tests and (optional) live showcase are **separate metrics**.",
        "Runtime Contracts N=13 is the verify.sh contract list only.",
        "",
        "## 4. Raw Results",
        "",
        f"- Run directory: `{meta.get('run_dir')}`",
        f"- Normalized records: {len(normalized)}",
        "",
        "## 5. Metrics",
        "",
        "| Metric | Value | N | Source | Formula |",
        "|---|---:|---:|---|---|",
    ]
    for m in metrics:
        val = m.get("value")
        if isinstance(val, float):
            val_s = f"{val:.4f}"
        else:
            val_s = str(val)
        lines.append(
            f"| {m['name']} | {val_s} | {m['sample_count']} | `{m['source']}` | {m['formula']} |"
        )
    lines += ["", "## 6. Failures / Exceptions", ""]
    if not failures:
        lines.append("None in this run.")
    else:
        for f in failures:
            lines.append(f"- `{f.get('test_name')}` ({f.get('package')}): {f.get('output', '')[:400]}")
    lines += [
        "",
        "## 7. Reproduction",
        "",
        "```text",
        "make db-up db-migrate",
        "python3 scripts/collect_verify.py",
        "```",
        "",
        "## 8. Conclusion",
        "",
        "Numbers in this file are computed from `metrics.json` / `normalized.json` of this run.",
        "See `TEST_DATA.md` for lineage.",
        "",
    ]
    return "\n".join(lines)


def render_test_data(meta: dict, metrics: list[dict], catalog: dict) -> str:
    lines = [
        "# Test Data",
        "",
        "每个写入 README / 面试的数字必须能沿这条链反查。",
        "",
        f"- commit: `{meta.get('commit_sha')}`",
        f"- run: `{meta.get('run_dir')}`",
        "",
        "## Runtime Contracts (13)",
        "",
        "公式：`passed / 13`，分母固定为 catalog `runtime_contracts`，不是全部 go test。",
        "",
    ]
    for c in catalog.get("runtime_contracts", []):
        lines.append(f"- `{c['test_name']}` — {c['capability']} — `{c['test_file']}`")
    lines += ["", "## Metrics lineage", ""]
    for m in metrics:
        lines.append(f"### {m['name']}")
        lines.append("")
        lines.append(f"- value: `{m['value']}`")
        lines.append(f"- N: `{m['sample_count']}`")
        lines.append(f"- formula: `{m['formula']}`")
        lines.append(f"- source: `{m['source']}`")
        if m.get("cases"):
            lines.append("- cases:")
            for case in m["cases"]:
                lines.append(f"  - `{case}`")
        lines.append("")
    return "\n".join(lines)


def go_test_json(run_pat: str, packages: list[str], out_path: Path, timeout: int = 180) -> tuple[int, str]:
    args = ["go", "test", "-json", "-p", "1", "-count=1", f"-timeout={timeout}s", "-v", "-run", run_pat, *packages]
    p = run_cmd(args, cwd=ROOT, timeout=timeout + 60)
    text = redact((p.stdout or "") + "\n" + (p.stderr or ""))
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(text, encoding="utf-8")
    return p.returncode, text


def ensure_db() -> None:
    if shutil.which("make"):
        run_cmd(["make", "db-up", "db-migrate"], cwd=ROOT, timeout=180)


def collect(smoke: bool, skip_go_full: bool) -> Path:
    catalog = load_catalog()
    stamp = utc_stamp()
    run_dir = ROOT / "artifacts" / "runs" / stamp
    raw = run_dir / "raw"
    raw.mkdir(parents=True, exist_ok=True)
    (raw / "contracts").mkdir(exist_ok=True)

    commands: list[str] = []
    meta = {
        "repository": "desk",
        "commit_sha": git_sha(),
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "command": "python3 scripts/collect_verify.py" + (" --smoke" if smoke else ""),
        "commands": commands,
        "environment": sanitize_env(),
        "model": {
            "provider": "deepseek",
            "flash_model": os.environ.get("DESK_FLASH_MODEL", ""),
            "pro_model": os.environ.get("DESK_PRO_MODEL", ""),
        },
        "go_version": go_version(),
        "smoke": smoke,
        "run_dir": str(run_dir.relative_to(ROOT)),
    }
    write_json(run_dir / "meta.json", meta)

    normalized: list[dict[str, Any]] = []
    failures: list[dict[str, Any]] = []
    measurements: list[dict[str, Any]] = []

    def add_norm(**kwargs: Any) -> None:
        rec = {
            "repository": "desk",
            "commit_sha": meta["commit_sha"],
            "timestamp": meta["timestamp"],
            **kwargs,
        }
        normalized.append(rec)
        if rec.get("pass_fail") == "fail":
            failures.append(rec)

    if not smoke:
        ensure_db()
        commands.append("make db-up db-migrate")

    if smoke:
        c0 = catalog["runtime_contracts"][0]
        rc, text = go_test_json(
            f"^{c0['test_name']}$",
            [c0["package"]],
            raw / "contracts" / f"{c0['test_name']}.jsonl",
        )
        commands.append(f"go test -json -run '^{c0['test_name']}$' {c0['package']}")
        rows = top_level_tests(parse_go_jsonl(raw / "contracts" / f"{c0['test_name']}.jsonl"))
        status = "pass" if rc == 0 and any(r["Action"] == "pass" for r in rows) else "fail"
        add_norm(
            test_name=c0["test_name"],
            test_file=c0["test_file"],
            test_command=commands[-1],
            package=c0["package"],
            capability=c0["capability"],
            suite="runtime_contract_smoke",
            pass_fail=status,
            evidence=extract_evidence(text),
            measurements=extract_measurements(text),
        )
        measurements.extend(extract_measurements(text))
    else:
        if not skip_go_full:
            rc, text = go_test_json(".", GO_PACKAGES, raw / "integration.jsonl", timeout=180)
            commands.append("go test -json -p 1 -count=1 -timeout 180s ./cmd/... ./internal/... ./plugins/...")
            rows = top_level_tests(parse_go_jsonl(raw / "integration.jsonl"))
            for r in rows:
                add_norm(
                    test_name=r.get("Test"),
                    test_file="",
                    test_command=commands[-1],
                    package=r.get("Package"),
                    suite="go_integration",
                    pass_fail="pass" if r.get("Action") == "pass" else ("skip" if r.get("Action") == "skip" else "fail"),
                    elapsed_s=r.get("Elapsed"),
                )
            (raw / "integration.exit").write_text(str(rc), encoding="utf-8")

        for c in catalog["runtime_contracts"]:
            rc, text = go_test_json(
                f"^{c['test_name']}$",
                [c["package"]],
                raw / "contracts" / f"{c['test_name']}.jsonl",
            )
            cmd = f"go test -json -run '^{c['test_name']}$' {c['package']}"
            commands.append(cmd)
            rows = top_level_tests(parse_go_jsonl(raw / "contracts" / f"{c['test_name']}.jsonl"))
            status = "pass" if rc == 0 and any(x["Action"] == "pass" for x in rows) else "fail"
            add_norm(
                test_name=c["test_name"],
                test_file=c["test_file"],
                test_command=cmd,
                package=c["package"],
                capability=c["capability"],
                suite="runtime_contract",
                pass_fail=status,
                evidence=extract_evidence(text),
                measurements=extract_measurements(text),
            )

        for c in catalog["extra_worker_protocol"]:
            rc, text = go_test_json(
                f"^{c['test_name']}$",
                [c["package"]],
                raw / "extra" / f"{c['test_name']}.jsonl",
            )
            cmd = f"go test -json -run '^{c['test_name']}$' {c['package']}"
            commands.append(cmd)
            rows = top_level_tests(parse_go_jsonl(raw / "extra" / f"{c['test_name']}.jsonl"))
            status = "pass" if rc == 0 and any(x["Action"] == "pass" for x in rows) else "fail"
            add_norm(
                test_name=c["test_name"],
                test_file=c["test_file"],
                test_command=cmd,
                package=c["package"],
                capability=c["capability"],
                suite="worker_protocol_extra",
                pass_fail=status,
                evidence=extract_evidence(text),
                measurements=extract_measurements(text),
            )

        rc, text = go_test_json(
            "TestEvicted|TestInvariant|TestPrepareSmallCompact|TestLargeRolling|TestTotalBudget",
            ["./internal/ctxmgr/"],
            raw / "ctxmgr.jsonl",
        )
        commands.append("go test -json -run 'TestEvictedBuffer|TestInvariant|...' ./internal/ctxmgr/")
        measurements.extend(extract_measurements(text))
        rows = top_level_tests(parse_go_jsonl(raw / "ctxmgr.jsonl"))
        for r in rows:
            add_norm(
                test_name=r.get("Test"),
                test_file="internal/ctxmgr/",
                test_command=commands[-1],
                package=r.get("Package"),
                suite="context_invariant",
                capability="Context Compact" if "Compact" in str(r.get("Test")) or "Invariant" in str(r.get("Test")) else "Evicted Buffer",
                pass_fail="pass" if r.get("Action") == "pass" else ("skip" if r.get("Action") == "skip" else "fail"),
                measurements=extract_measurements(text) if r.get("Test", "").startswith("TestEvicted") else [],
            )

    showcase_path = Path(os.environ.get("DESK_SHOWCASE_JSON", ""))
    live_metric = None
    if showcase_path.is_file():
        shutil.copy(showcase_path, raw / "showcase.json")
        show = json.loads(showcase_path.read_text(encoding="utf-8"))
        runs = show.get("runs") or []
        ok = sum(1 for r in runs if r.get("status") == "completed")
        live_metric = metric(
            "live_showcase_success_rate",
            f"{ok}/{len(runs)}" if runs else "0/0",
            n=len(runs),
            formula="completed_runs / showcase_runs (from showcase.json, not go test)",
            source="raw/showcase.json",
            cases=[str(r.get("run_id")) for r in runs],
        )
        add_norm(
            test_name="showcase_live",
            test_file="scripts/showcase_live.py",
            test_command="make showcase-live-auto",
            suite="live_showcase",
            pass_fail="pass" if show.get("overall") == "PASS" else "fail",
            measurement=show,
        )

    contracts = [n for n in normalized if n.get("suite") in {"runtime_contract", "runtime_contract_smoke"}]
    c_pass = sum(1 for n in contracts if n.get("pass_fail") == "pass")
    c_n = 13 if not smoke else len(contracts)
    denom = 13 if not smoke else max(len(contracts), 1)

    go_rows = [n for n in normalized if n.get("suite") == "go_integration"]
    go_c = count_status(
        [{"Action": "pass" if n["pass_fail"] == "pass" else ("skip" if n["pass_fail"] == "skip" else "fail")} for n in go_rows]
    )

    ctx_rows = [n for n in normalized if n.get("suite") == "context_invariant"]
    ctx_pass = sum(1 for n in ctx_rows if n["pass_fail"] == "pass")
    extra_rows = [n for n in normalized if n.get("suite") == "worker_protocol_extra"]
    extra_pass = sum(1 for n in extra_rows if n["pass_fail"] == "pass")

    metrics: list[dict[str, Any]] = [
        metric(
            "runtime_contract_pass_rate",
            f"{c_pass}/{denom}" if not smoke else f"{c_pass}/{len(contracts)}",
            n=len(contracts),
            formula="passed_named_contracts / 13 (catalog runtime_contracts); smoke uses N=ran",
            source="raw/contracts/*.jsonl",
            cases=[n["test_name"] for n in contracts],
        ),
        metric(
            "go_integration_pass_rate",
            f"{go_c['passed']}/{go_c['passed'] + go_c['failed']}" if go_rows else "NOT_RUN",
            n=len(go_rows),
            formula="top_level Test pass / (pass+fail); skips excluded from denominator",
            source="raw/integration.jsonl",
            cases=[n["test_name"] for n in go_rows if n["pass_fail"] == "fail"],
        ),
        metric(
            "context_invariant_pass_rate",
            f"{ctx_pass}/{len(ctx_rows)}" if ctx_rows else "NOT_RUN",
            n=len(ctx_rows),
            formula="ctxmgr invariant/evicted/compact tests pass / total ran",
            source="raw/ctxmgr.jsonl",
            cases=[n["test_name"] for n in ctx_rows],
        ),
        metric(
            "worker_protocol_extra_pass_rate",
            f"{extra_pass}/{len(extra_rows)}" if extra_rows else ("NOT_RUN" if not smoke else "NOT_RUN"),
            n=len(extra_rows),
            formula="TurnFinishSchema + HTTP approval/cancel pass / N; NOT part of 13/13",
            source="raw/extra/*.jsonl",
            cases=[n["test_name"] for n in extra_rows],
        ),
    ]
    if live_metric:
        metrics.append(live_metric)
    else:
        metrics.append(
            metric(
                "live_showcase_success_rate",
                "NOT_RUN",
                n=0,
                formula="completed_runs / 4 from scripts/showcase_live.py artifact",
                source="(absent)",
            )
        )

    write_json(run_dir / "normalized.json", normalized)
    write_json(run_dir / "metrics.json", {"metrics": metrics, "measurements": measurements})
    write_json(run_dir / "meta.json", meta)
    (run_dir / "report.md").write_text(render_report(meta, metrics, normalized, failures), encoding="utf-8")
    (run_dir / "TEST_DATA.fragment.md").write_text(render_test_data(meta, metrics, catalog), encoding="utf-8")

    latest = ROOT / "artifacts" / "latest"
    if latest.is_symlink() or latest.exists():
        latest.unlink()
    latest.symlink_to(Path("runs") / stamp)

    (ROOT / "TEST_REPORT.md").write_text((run_dir / "report.md").read_text(encoding="utf-8"), encoding="utf-8")
    (ROOT / "TEST_DATA.md").write_text(render_test_data(meta, metrics, catalog), encoding="utf-8")
    return run_dir


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--smoke", action="store_true")
    ap.add_argument("--skip-go-full", action="store_true")
    args = ap.parse_args()
    run_dir = collect(smoke=args.smoke, skip_go_full=args.skip_go_full)
    print("artifact", run_dir)
    metrics = json.loads((run_dir / "metrics.json").read_text(encoding="utf-8"))
    for m in metrics["metrics"]:
        print(f"{m['name']}={m['value']} n={m['sample_count']}")
    fails = [n for n in json.loads((run_dir / "normalized.json").read_text()) if n.get("pass_fail") == "fail"]
    return 1 if fails and not args.smoke else (1 if fails else 0)


if __name__ == "__main__":
    raise SystemExit(main())
