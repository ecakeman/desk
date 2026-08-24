import json
import sys

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    t = msg.get("t")
    if t == "turn.start":
        out = {
            "t": "tool.request",
            "id": "1",
            "name": "fs.read",
            "args": {"path": "README.md"},
        }
    elif t in ("tool.result","tool.denied"):
        out = {"t": "turn.finish"}
    else:
        out = {"t": "turn.fail", "error": "unknown t: " + str(t)}
    print(json.dumps(out), flush=True)