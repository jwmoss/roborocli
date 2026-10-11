#!/usr/bin/env python3
"""Read the configured live service through the compiled CLI."""

import json
from pathlib import Path
import subprocess
import sys

binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else "bin/roborocli").resolve())


def read(*args):
    try:
        result = subprocess.run(
            [binary, "--no-input", "--timeout", "15s", "--json", *args],
            capture_output=True, text=True, timeout=60, check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        raise SystemExit("Live check failed: executable unavailable or deadline exceeded.") from None
    if result.returncode:
        raise SystemExit(f"Live check failed: CLI exit {result.returncode}. Check service access and saved credentials locally.")
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError:
        raise SystemExit("Live check failed: command did not return JSON.") from None


def require(condition, message):
    if not condition:
        raise SystemExit("Live check failed: " + message)


report = read("--dry-run", "command", "get_status")
require(isinstance(report, dict) and report.get("method") == "get_status", "missing RPC receipt")
require(report.get("transport") in ("local", "mqtt"), "no direct device response")
status = report.get("result")
if isinstance(status, list) and len(status) == 1:
    status = status[0]
require(isinstance(status, dict), "missing device status")
require(type(status.get("battery")) is int and 0 <= status["battery"] <= 100, "invalid battery reading")
require(type(status.get("state")) is int, "missing device state")
print("Live E2E passed: current device status received over " + report["transport"] + ".")
