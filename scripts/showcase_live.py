#!/usr/bin/env python3
"""Live Showcase：真实模型 + 真实 Runtime。不使用 fake worker。"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
FIXTURE = ROOT / "fixtures" / "bookmark-lab"
TERMINAL = {"completed", "failed", "interrupted"}
BASE = os.environ.get("DESK_SHOWCASE_URL", "http://127.0.0.1:8080").rstrip("/")
AUTO = os.environ.get("SHOWCASE_AUTO", "").strip() in {"1", "true", "yes"}
RUN_TIMEOUT = float(os.environ.get("SHOWCASE_RUN_TIMEOUT", "600"))

PROMPTS = [
    (
        "请先了解 bookmark-lab 当前的实现和现状。\n"
        "我准备继续改这个项目，但现在还不急着动代码。\n"
        "先建立你对当前项目结构、已有功能、当前约束和历史决定的理解。\n"
        "如果发现值得持续跟踪的事项，请建立合适的 task。"
    ),
    (
        "在现有 Bookmark Manager 基础上增加可选的 collection（收藏夹）字段："
        "每个书签可以属于一个收藏夹，也可以不属于任何收藏夹。\n"
        "先考虑这是否和现有 tags 冲突，再完成必要的文档修改。\n"
        "保留已有行为，不要破坏现有功能，也不要擅自关闭仍开放的问题。"
    ),
    (
        "继续处理刚才的 Bookmark Manager。\n"
        "结合之前已经确定的方案，检查当前实现还有哪些遗漏，"
        "完成这次变更剩余的工作。\n"
        "不要重新推翻之前已经确定的设计。"
    ),
    (
        "请回顾当前 Bookmark Manager 的修改，"
        "检查实现是否符合之前确定的要求，"
        "找出遗漏、冲突或不合理的地方，"
        "完成必要的修正并收口。"
    ),
]


def req(method: str, path: str, body: object | None = None, timeout: float = 30) -> object:
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(
        BASE + path,
        data=data,
        method=method,
        headers={"Content-Type": "application/json", "Accept": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=timeout) as resp:
        raw = resp.read()
        return json.loads(raw.decode()) if raw else None


def healthz() -> bool:
    try:
        out = req("GET", "/healthz", timeout=3)
        return isinstance(out, dict) and out.get("ok") is True
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError):
        return False


def workspace_root() -> Path:
    out = req("GET", "/v1/workspace")
    if not isinstance(out, dict) or not out.get("path"):
        raise SystemExit("GET /v1/workspace failed")
    return Path(str(out["path"]))


def reset_lab(work: Path) -> Path:
    dest = work / "bookmark-lab"
    if dest.exists():
        shutil.rmtree(dest)
    shutil.copytree(FIXTURE, dest)
    return dest


def tree_hash(root: Path) -> dict[str, str]:
    out: dict[str, str] = {}
    if not root.exists():
        return out
    for p in sorted(root.rglob("*")):
        if p.is_file():
            rel = str(p.relative_to(root))
            out[rel] = hashlib.sha256(p.read_bytes()).hexdigest()
    return out


def diff_files(before: dict[str, str], after: dict[str, str]) -> list[str]:
    changed = []
    for k, v in after.items():
        if before.get(k) != v:
            changed.append(k)
    for k in before:
        if k not in after:
            changed.append(k + " (deleted)")
    return sorted(changed)


def run_events(session_id: str, run_id: str) -> list[dict]:
    items = req("GET", f"/v1/sessions/{session_id}/events")
    if not isinstance(items, list):
        return []
    return [e for e in items if e.get("run_id") == run_id]


def pending_seq(events: list[dict]) -> int | None:
    closed: set[int] = set()
    requested: list[int] = []
    for e in events:
        payload = e.get("payload") or {}
        typ = e.get("type")
        seq = e.get("seq")
        if typ == "tool.requested" and isinstance(seq, int):
            requested.append(seq)
        if typ in {"tool.completed", "tool.denied", "tool.failed"}:
            if isinstance(seq, int):
                closed.add(seq)
            if isinstance(payload, dict) and isinstance(payload.get("seq"), int):
                closed.add(payload["seq"])
    for seq in reversed(requested):
        if seq not in closed:
            return seq
    return None


def decide(run_id: str, seq: int, name: str, args: object) -> None:
    print(f"\n[approval] {name} {args}")
    if AUTO:
        print("decision: allow (auto)")
        allow = True
    else:
        ans = input("allow? [y/n] ").strip().lower()
        allow = ans == "y"
        print("decision:", "allow" if allow else "reject")
    try:
        req("POST", f"/v1/runs/{run_id}/decisions", {"seq": seq, "allow": allow})
    except urllib.error.HTTPError as exc:
        if exc.code != 409:
            raise
        print("decision already consumed")


def follow_run(run_id: str, session_id: str) -> str:
    """轮询事件与 Run 状态；waiting_approval 时走真实 Decide。"""
    deadline = time.time() + RUN_TIMEOUT
    seen = 0
    while time.time() < deadline:
        events = run_events(session_id, run_id)
        for e in events:
            seq = int(e.get("seq") or 0)
            if seq <= seen:
                continue
            seen = seq
            print_event(e)
        st = req("GET", f"/v1/runs/{run_id}")
        if not isinstance(st, dict):
            time.sleep(0.4)
            continue
        status = str(st.get("status") or "")
        if status == "waiting_approval":
            seq_p = pending_seq(events)
            if seq_p is not None:
                name = "tool"
                args: object = {}
                for e in events:
                    if e.get("seq") == seq_p:
                        p = e.get("payload") or {}
                        name = str(p.get("name") or "tool")
                        args = p.get("args") or {}
                decide(run_id, seq_p, name, args)
        if status in TERMINAL:
            return status
        time.sleep(0.35)
    raise SystemExit(f"run {run_id} timeout")


def print_event(e: dict) -> None:
    typ = e.get("type")
    payload = e.get("payload") or {}
    if typ == "message.delta":
        sys.stdout.write(str(payload.get("text") or ""))
        sys.stdout.flush()
    elif typ == "message.completed":
        print()
        print("[assistant]", (payload.get("text") or "")[:500])
    elif typ == "tool.requested":
        print(f"\n[tool.requested] {payload.get('name')} seq={e.get('seq')}")
    elif typ == "tool.started":
        print(f"[tool.started] {payload.get('name')}")
    elif typ == "tool.completed":
        print(f"[tool.completed] {payload.get('name')}")
    elif typ == "tool.denied":
        print(f"[tool.denied] {payload.get('name')}")
    elif typ == "tool.failed":
        print(f"[tool.failed] {payload.get('name')}")
    elif typ == "task.updated":
        print(f"[task] {payload.get('status')} {payload.get('title')}")
    elif typ == "memory.retrieved":
        print("[memory] retrieved")
    elif typ == "review.completed":
        print("[review]", payload.get("summary") or "completed")
    elif typ in {"run.completed", "run.failed", "run.interrupted"}:
        print()
        print("[run]", typ)


def count_type(events: list[dict], typ: str) -> int:
    return sum(1 for e in events if e.get("type") == typ)


def project_status(events: list[dict]) -> str:
    requested: set[str] = set()
    started: set[str] = set()
    open_ask: set[str] = set()
    terminal = ""
    ordered = sorted(events, key=lambda e: int(e.get("seq") or 0))
    for i, e in enumerate(ordered):
        seq = int(e.get("seq") or 0)
        if seq != i + 1:
            raise ValueError(f"seq gap {seq} at {i}")
        if terminal:
            raise ValueError(f"{e.get('type')} after {terminal}")
        payload = e.get("payload") or {}
        tid = str(payload.get("id") or "")
        typ = e.get("type")
        if typ == "run.completed":
            terminal = "completed"
        elif typ == "run.failed":
            terminal = "failed"
        elif typ == "run.interrupted":
            terminal = "interrupted"
        elif typ == "tool.requested":
            requested.add(tid)
            open_ask.add(tid)
        elif typ == "tool.started":
            if tid not in requested:
                raise ValueError("started without requested")
            started.add(tid)
            open_ask.discard(tid)
        elif typ == "tool.completed":
            if tid not in started:
                raise ValueError("completed without started")
            open_ask.discard(tid)
        elif typ in {"tool.denied", "tool.failed"}:
            if tid not in requested:
                raise ValueError(f"{typ} without requested")
            open_ask.discard(tid)
    if terminal:
        return terminal
    if open_ask:
        return "waiting_approval"
    return "running"


def usage_slots(events: list[dict]) -> tuple[int, int, int]:
    flash = pro = review = 0
    for e in events:
        if e.get("type") != "model.usage":
            continue
        p = e.get("payload") or {}
        model = str(p.get("model") or "")
        phase = str(p.get("phase") or "")
        if model == "flash":
            flash += 1
        elif model == "pro":
            pro += 1
        if phase == "review":
            review += 1
    return flash, pro, review


def memory_hits(events: list[dict]) -> int:
    n = 0
    for e in events:
        if e.get("type") != "tool.completed":
            continue
        p = e.get("payload") or {}
        if p.get("name") != "memory.search":
            continue
        data = p.get("data") or {}
        if isinstance(data, str):
            try:
                data = json.loads(data)
            except json.JSONDecodeError:
                data = {}
        hits = data.get("hits") if isinstance(data, dict) else None
        if isinstance(hits, list):
            n += len(hits)
    for e in events:
        if e.get("type") != "memory.retrieved":
            continue
        p = e.get("payload") or {}
        hits = p.get("hits")
        if isinstance(hits, list):
            n += len(hits)
    return n


def print_run(i: int, status: str, events: list[dict], changed: list[str]) -> None:
    flash, pro, reviews = usage_slots(events)
    print()
    print(f"Run {i}")
    print("────────────────────────────")
    print(f"status             {status}")
    print(f"events             {len(events)}")
    print(f"tool_requested     {count_type(events, 'tool.requested')}")
    print(f"tool_started       {count_type(events, 'tool.started')}")
    print(f"tool_completed     {count_type(events, 'tool.completed')}")
    print(f"approvals          {count_approvals(events)}")
    print(f"workspace_changes  {len(changed)}")
    print(f"memory_search      {sum(1 for e in events if (e.get('payload') or {}).get('name') == 'memory.search')}")
    print(f"memory_hits        {memory_hits(events)}")
    print(f"task_updates       {count_type(events, 'task.updated')}")
    print(f"flash_usage        {flash}")
    print(f"pro_usage          {pro}")
    print(f"reviews            {count_type(events, 'review.completed')}")
    if changed:
        print("files              " + ", ".join(changed[:8]))


def count_approvals(events: list[dict]) -> int:
    """waiting_approval 之后出现 started/denied 的次数。"""
    req_seq = []
    closed = 0
    for e in sorted(events, key=lambda x: int(x.get("seq") or 0)):
        typ = e.get("type")
        if typ == "tool.requested":
            req_seq.append(int(e.get("seq") or 0))
        elif typ in {"tool.started", "tool.denied"}:
            closed += 1
    # 写工具才会 Ask；用 denied+started 中跟在 requested 之后的对数并不精确。
    # 更准：tool.denied 或 (requested 且随后 started 且 name=fs.write)
    n = 0
    pending_write = False
    for e in sorted(events, key=lambda x: int(x.get("seq") or 0)):
        p = e.get("payload") or {}
        typ = e.get("type")
        if typ == "tool.requested" and str(p.get("name") or "").endswith(".write"):
            pending_write = True
        elif pending_write and typ in {"tool.started", "tool.denied"}:
            n += 1
            pending_write = False
    return n


def main() -> int:
    if "--reset-only" in sys.argv:
        dest = ROOT / "ws-probe" / "bookmark-lab"
        dest.parent.mkdir(parents=True, exist_ok=True)
        if dest.exists():
            shutil.rmtree(dest)
        shutil.copytree(FIXTURE, dest)
        print("reset", dest)
        if healthz():
            lab = reset_lab(workspace_root())
            print("reset server workspace", lab)
        return 0

    if not healthz():
        print("desk serve is not reachable at", BASE, file=sys.stderr)
        print("start: DESK_WORKSPACE=ws-probe ./bin/desk serve", file=sys.stderr)
        return 2

    work = workspace_root()
    lab = reset_lab(work)
    print("workspace", work)
    print("reset", lab)
    baseline = tree_hash(lab)

    sess = req("POST", "/v1/sessions")
    if not isinstance(sess, dict) or not sess.get("id"):
        print("create session failed", sess, file=sys.stderr)
        return 2
    session_id = str(sess["id"])
    print("session", session_id)
    print("mode", "auto-allow" if AUTO else "interactive y/n")

    runs: list[dict] = []
    prev_hash = baseline
    all_events: list[dict] = []

    for i, prompt in enumerate(PROMPTS, start=1):
        print()
        print(f"======== Run {i} ========")
        print(prompt.split("\n")[0])
        created = req("POST", f"/v1/sessions/{session_id}/messages", {"text": prompt})
        if not isinstance(created, dict) or not created.get("run_id"):
            print("post message failed", created, file=sys.stderr)
            return 2
        run_id = str(created["run_id"])
        print("run_id", run_id)
        try:
            status = follow_run(run_id, session_id)
        except Exception as exc:
            print("follow error:", exc, file=sys.stderr)
            st = req("GET", f"/v1/runs/{run_id}")
            status = str(st.get("status") if isinstance(st, dict) else "failed")
        events = run_events(session_id, run_id)
        all_events.extend(events)
        now = tree_hash(lab)
        changed = diff_files(prev_hash, now)
        prev_hash = now
        rec = {
            "i": i,
            "run_id": run_id,
            "status": status,
            "events": events,
            "changed": changed,
            "approvals": count_approvals(events),
        }
        runs.append(rec)
        print_run(i, status, events, changed)

    consistent = True
    for rec in runs:
        evs = rec["events"]
        try:
            proj = project_status(evs)
        except ValueError as exc:
            print("project error", rec["run_id"], exc)
            consistent = False
            continue
        if proj != rec["status"]:
            print("inconsistent", rec["run_id"], proj, rec["status"])
            consistent = False

    terminal_ok = sum(1 for r in runs if r["status"] in TERMINAL)
    completed_ok = sum(1 for r in runs if r["status"] == "completed")
    total_changed = diff_files(baseline, tree_hash(lab))
    total_approvals = sum(int(r["approvals"]) for r in runs)
    tasks = sum(count_type(r["events"], "task.updated") for r in runs)
    mem_search = sum(
        1
        for r in runs
        for e in r["events"]
        if (e.get("payload") or {}).get("name") == "memory.search"
    )
    mem_hits = sum(memory_hits(r["events"]) for r in runs)
    reviews = sum(count_type(r["events"], "review.completed") for r in runs)
    budget_ok = reviews <= 2 * len(runs)

    task_c = "PASS" if tasks > 0 else "UNUSED"
    mem_c = "PASS" if mem_hits > 0 or mem_search > 0 else "UNUSED"
    approval_c = "PASS" if total_approvals > 0 else "MISSING"
    ws_c = "PASS" if total_changed else "MISSING"

    runtime_fail = completed_ok != 4 or not consistent
    model_fail = approval_c == "MISSING" or ws_c == "MISSING"
    overall = "PASS"
    kind = ""
    if runtime_fail:
        overall = "FAIL"
        kind = "Runtime defect" if not consistent or terminal_ok != 4 else "Runtime defect"
    elif model_fail:
        overall = "FAIL"
        kind = "Model behavior"

    print()
    print("Desk V1 Pro Live Showcase")
    print("────────────────────────────────────")
    print()
    print("Session")
    print("  1 session")
    print(f"  {len(runs)} consecutive runs")
    print()
    for rec in runs:
        evs = rec["events"]
        print(f"Run {rec['i']}")
        print(f"  status              {rec['status']}")
        print(f"  tools               {count_type(evs, 'tool.requested')}")
        print(f"  memory              {memory_hits(evs)}")
        print(f"  tasks               {count_type(evs, 'task.updated')}")
        print(f"  approvals           {rec['approvals']}")
        print(f"  workspace_changes   {len(rec['changed'])}")
        print()
    print("Runtime")
    print(f"  Event consistency   {'PASS' if consistent else 'FAIL'}")
    print(f"  Approval boundary   {approval_c}")
    print(f"  Workspace mutation  {ws_c}")
    print(f"  Tool lifecycle      {'PASS' if all(count_type(r['events'], 'tool.completed') <= count_type(r['events'], 'tool.started') for r in runs) else 'FAIL'}")
    print(f"  Session continuity  {'PASS' if len(runs) == 4 else 'FAIL'}")
    print(f"  Task continuity     {task_c}")
    print(f"  Memory continuity   {mem_c}")
    print(f"  Review count        {reviews}")
    print(f"  Review budget       {'PASS' if budget_ok else 'FAIL'}")
    print()
    print("Overall")
    print(f"  LIVE SHOWCASE       {overall}")
    if kind:
        print(f"  classification      {kind}")
        if approval_c == "MISSING":
            print("  failure             no fs.write approval observed")
        if ws_c == "MISSING":
            print("  failure             workspace unchanged vs fixture")
        if completed_ok != 4:
            print("  failure             not all runs completed")
        if not consistent:
            print("  failure             event/status projection mismatch")
    print()
    print("changed files:", ", ".join(total_changed) if total_changed else "(none)")
    return 0 if overall == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
