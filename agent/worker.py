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
        out.append({
            "type": "function",
            "function": {
                "name": api,
                "description": t.get("description") or "",
                "parameters": {
                    "type": "object",
                    "additionalProperties": True,
                },
            },
        })
    return out


def chat():
    body = {
        "model": MODEL,
        "stream": True,
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
    tcs = {}
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
                delta = (obj.get("choices") or [{}])[0].get("delta") or {}
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
        slot = tcs[min(tcs)]
        try:
            args = json.loads(slot["arguments"] or "{}")
        except json.JSONDecodeError:
            return {"t": "turn.fail", "error": "bad_tool_args"}
        if not isinstance(args, dict):
            args = {}
        api_name = slot["name"]
        messages.append({
            "role": "assistant",
            "tool_calls": [{
                "id": slot["id"] or "1",
                "type": "function",
                "function": {"name": api_name, "arguments": slot["arguments"]},
            }],
        })
        return {
            "t": "tool.request",
            "id": slot["id"] or "1",
            "name": api_to_host.get(api_name, api_name),
            "args": args,
        }
    text = "".join(acc)
    messages.append({"role": "assistant", "content": text})
    return {"t": "turn.finish", "text": text}


for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    t = msg.get("t")
    if t == "turn.start":
        messages = [
            {
                "role": "system",
                "content": "只能通过工具查看 Workspace。不要编造文件内容。",
            }
        ]
        messages.extend(msg.get("messages") or [])
        tools = openai_tools(msg.get("tools"))
        out = chat()
    elif t == "tool.result":
        messages.append({
            "role": "tool",
            "tool_call_id": msg.get("id"),
            "content": msg.get("data") if isinstance(msg.get("data"), str) else json.dumps(msg.get("data")),
        })
        out = chat()
    elif t == "tool.denied":
        messages.append({
            "role": "tool",
            "tool_call_id": msg.get("id"),
            "content": "denied",
        })
        out = chat()
    else:
        out = {"t": "turn.fail", "error": "unknown t: " + str(t)}
    print(json.dumps(out, ensure_ascii=False), flush=True)