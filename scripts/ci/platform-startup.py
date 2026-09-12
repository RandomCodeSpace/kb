#!/usr/bin/env python3
"""Start the built web and MCP commands against disposable platform-smoke data."""

import json
import queue
import subprocess
import sys
import threading
import urllib.request

binary, data = sys.argv[1:]


def read_line(process):
    result = queue.Queue()
    threading.Thread(target=lambda: result.put(process.stdout.readline()), daemon=True).start()
    try:
        line = result.get(timeout=15)
    except queue.Empty:
        raise RuntimeError("command did not answer within 15 seconds") from None
    if not line:
        raise RuntimeError("command exited before answering")
    return line


def stop(process):
    if process.poll() is None:
        process.kill()
    process.wait(timeout=10)


web = subprocess.Popen(
    [binary, "web", "--data", data, "--no-open", "--addr", "127.0.0.1:0"],
    stdout=subprocess.PIPE, text=True,
)
try:
    line = read_line(web).strip()
    prefix = "kb web: serving "
    if not line.startswith(prefix):
        raise RuntimeError(f"unexpected web startup output: {line!r}")
    with urllib.request.urlopen(line[len(prefix):] + "/api/meta", timeout=10) as response:
        metadata = json.load(response)
    if not isinstance(metadata, dict) or "version" not in metadata:
        raise RuntimeError(f"unexpected web metadata: {metadata!r}")
finally:
    stop(web)

mcp = subprocess.Popen(
    [binary, "mcp", "--data", data],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True,
)
try:
    request = {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {
            "protocolVersion": "2024-11-05", "capabilities": {},
            "clientInfo": {"name": "kb-platform-smoke", "version": "1"},
        },
    }
    mcp.stdin.write(json.dumps(request) + "\n")
    mcp.stdin.flush()
    response = json.loads(read_line(mcp))
    if response.get("id") != 1 or "serverInfo" not in response.get("result", {}):
        raise RuntimeError(f"unexpected MCP initialize response: {response!r}")
finally:
    stop(mcp)

print("platform-smoke: web HTTP and MCP stdio startup pass")
