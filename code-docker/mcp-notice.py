#!/usr/bin/env python3
"""Runs chrome-devtools-mcp and tells every client how this browser is set up.

chrome-devtools-mcp has no idea that Chrome here is a separate container, that
localhost in it reaches code-docker only through a forward table, or how to change that
table. MCP's initialize response has an `instructions` field that clients put in front of
the model (Claude Code adds it to the system prompt), so this appends
chrome-devtools-notice.md there - after the server's own instructions, if it sends any.
Every other message passes through byte for byte. Same filter as roblox-studio-docker's
config/mcp/mcp-shared-notice.py.

Usage: mcp-notice.py <command> [args...]   (stdin and stderr go to the command)
"""
import json
import os
import signal
import subprocess
import sys

NOTICE_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "chrome-devtools-notice.md")


def add_notice(line: bytes, notice: str) -> bytes:
    try:
        message = json.loads(line)
    except ValueError:
        return line
    result = message.get("result") if isinstance(message, dict) else None
    # Only the initialize response carries both of these.
    if not isinstance(result, dict) or "protocolVersion" not in result or "serverInfo" not in result:
        return line
    existing = result.get("instructions")
    result["instructions"] = f"{existing}\n\n{notice}" if existing else notice
    return json.dumps(message, ensure_ascii=False).encode() + b"\n"


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: mcp-notice.py <command> [args...]", file=sys.stderr)
        return 2
    try:
        with open(NOTICE_FILE, encoding="utf-8") as f:
            notice = f.read().strip()
    except OSError as error:
        # The notice is advice, not a gate - run the server without it.
        print(f"[mcp-notice] {error}; passing messages through unchanged", file=sys.stderr)
        notice = ""

    child = subprocess.Popen(sys.argv[1:], stdout=subprocess.PIPE)
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, lambda signum, _frame: child.send_signal(signum))

    out = sys.stdout.buffer
    for line in child.stdout:
        out.write(add_notice(line, notice) if notice else line)
        out.flush()
    return child.wait()


if __name__ == "__main__":
    sys.exit(main())
