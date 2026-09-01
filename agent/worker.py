"""Desk 模型 Worker：每 Run 一个进程，stdin/stdout JSON line。不碰 DB 和 Workspace。"""

import json
import os
import sys
import urllib.error
import urllib.request

BASE = os.environ.get("DESK_MODEL_BASE_URL", "").rstrip("/")
KEY = os.environ.get("DESK_MODEL_API_KEY", "")
MODEL = os.environ.get("DESK_MODEL_MODEL", "")

messages = []
tools = []
api_to_host = {}


def apply_host_model(msg):
    """按本回合宿主指定的槽位改 API 地址。"""
    global BASE, KEY, MODEL
    if msg.get("base_url"):
        BASE = str(msg["base_url"]).rstrip("/")
    if "api_key" in msg and msg["api_key"] is not None:
        KEY = msg["api_key"]
    if msg.get("api_model"):
        MODEL = msg["api_model"]


def apply_host_system(msg):
    """安装稳定的 system；后续回合只会写入相同内容。"""
    system = msg.get("system")
    if not isinstance(system, str) or not system:
        return
    item = {"role": "system", "content": system}
    if messages and messages[0].get("role") == "system":
        messages[0] = item
    else:
        messages.insert(0, item)


def append_runtime(msg):
    """把当前 phase 策略作为动态数据追加到历史尾部。"""
    runtime = msg.get("runtime")
    if isinstance(runtime, str) and runtime:
        messages.append({"role": "user", "content": runtime})


def openai_tools(raw):
    global api_to_host
    out = []
    api_to_host = {}
    for t in raw or []:
        if not isinstance(t, dict) or not t.get("name"):
            continue
        host = t["name"]
        api = host.replace(".", "_")
        api_to_host[api] = host
        params = t.get("parameters")
        if not isinstance(params, dict):
            params = {"type": "object", "properties": {}}
        out.append({
            "type": "function",
            "function": {
                "name": api,
                "description": t.get("description") or "",
                "parameters": params,
            },
        })
    return out


def emit_usage(usage):
    """把供应商 usage 归一化成宿主可记录的字段。"""
    if not isinstance(usage, dict):
        return
    details = usage.get("prompt_tokens_details")
    if not isinstance(details, dict):
        details = {}
    item = {
        "t": "model.usage",
        "input_tokens": usage.get("prompt_tokens", usage.get("input_tokens", 0)) or 0,
        "output_tokens": usage.get("completion_tokens", usage.get("output_tokens", 0)) or 0,
        "cached_tokens": details.get("cached_tokens", usage.get("cached_tokens", 0)) or 0,
    }
    print(json.dumps(item, ensure_ascii=False), flush=True)


def chat():
    """对流式 chat/completions：内容分片回 message.delta，工具调用拼成 tool.request。"""
    body = {
        "model": MODEL,
        "stream": True,
        "stream_options": {"include_usage": True},
        "messages": messages,
    }
    if tools:
        body["tools"] = tools
    req = urllib.request.Request(
        BASE,
        data=json.dumps(body).encode(),
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer " + KEY,
        },
        method="POST",
    )
    acc = []
    reason = []
    tcs = {}
    usage = None
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            while True:
                raw = resp.readline()
                if not raw:
                    break
                line = raw.decode().strip()
                if not line.startswith("data:"):
                    continue
                data = line[5:].strip()
                if data == "[DONE]":
                    break
                obj = json.loads(data)
                if isinstance(obj.get("usage"), dict):
                    usage = obj["usage"]
                delta = (obj.get("choices") or [{}])[0].get("delta") or {}
                if delta.get("reasoning_content"):
                    reason.append(delta["reasoning_content"])
                if delta.get("content"):
                    piece = delta["content"]
                    acc.append(piece)
                    print(json.dumps({"t": "message.delta", "text": piece}, ensure_ascii=False), flush=True)
                for tc in delta.get("tool_calls") or []:
                    idx = tc.get("index", 0)
                    slot = tcs.setdefault(idx, {"id": "", "name": "", "arguments": ""})
                    if tc.get("id"):
                        slot["id"] = tc["id"]
                    fn = tc.get("function") or {}
                    if fn.get("name"):
                        slot["name"] += fn["name"]
                    if fn.get("arguments"):
                        slot["arguments"] += fn["arguments"]
    except urllib.error.HTTPError as e:
        return {"t": "turn.fail", "error": e.read().decode()[:500]}
    except Exception as e:
        return {"t": "turn.fail", "error": str(e)}

    if tcs:
        emit_usage(usage)
        slot = tcs[min(tcs)]
        try:
            args = json.loads(slot["arguments"] or "{}")
        except json.JSONDecodeError:
            return {"t": "turn.fail", "error": "bad_tool_args"}
        if not isinstance(args, dict):
            args = {}
        api_name = slot["name"]
        asst = {
            "role": "assistant",
            "tool_calls": [{
                "id": slot["id"] or "1",
                "type": "function",
                "function": {"name": api_name, "arguments": slot["arguments"]},
            }],
        }
        rs = "".join(reason)
        if rs:
            asst["reasoning_content"] = rs
        messages.append(asst)
        return {
            "t": "tool.request",
            "id": slot["id"] or "1",
            "name": api_to_host.get(api_name, api_name),
            "args": args,
        }
    text = "".join(acc)
    emit_usage(usage)
    asst = {"role": "assistant", "content": text}
    rs = "".join(reason)
    if rs:
        asst["reasoning_content"] = rs
    messages.append(asst)
    return {"t": "turn.finish", "text": text}


for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    apply_host_model(msg)
    t = msg.get("t")
    if t == "turn.start":
        messages = []
        messages.extend(msg.get("messages") or [])
        apply_host_system(msg)
        tools = openai_tools(msg.get("tools"))
        append_runtime(msg)
        out = chat()
    elif t == "tool.result":
        if msg.get("ok"):
            data = msg.get("data")
            content = data if isinstance(data, str) else json.dumps(data)
        else:
            content = msg.get("error") or "error"
        messages.append({
            "role": "tool",
            "tool_call_id": msg.get("id"),
            "content": content,
        })
        messages.extend(msg.get("messages") or [])
        apply_host_system(msg)
        append_runtime(msg)
        out = chat()
    elif t == "tool.denied":
        messages.append({
            "role": "tool",
            "tool_call_id": msg.get("id"),
            "content": "denied",
        })
        messages.extend(msg.get("messages") or [])
        apply_host_system(msg)
        append_runtime(msg)
        out = chat()
    else:
        out = {"t": "turn.fail", "error": "unknown t: " + str(t)}
    print(json.dumps(out, ensure_ascii=False), flush=True)