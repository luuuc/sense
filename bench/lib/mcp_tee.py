#!/usr/bin/env python3
"""Transparent MCP stdio capture shim (bench harness only — never part of Sense).

Usage:  mcp_tee.py [--log FILE] -- <server command...>
        e.g.  mcp_tee.py --log results/run/sense-io.jsonl -- sense mcp

Sits between an MCP client and the server it spawns, passing every byte
through UNCHANGED while appending each JSON-RPC frame to the log as one
JSONL record: {"ts": ..., "dir": "c2s"|"s2c", "msg": <frame>}. MCP stdio
framing is newline-delimited JSON, so pass-through is line-by-line.

Honesty properties (these are the contract; the fixture test asserts them):
  * Byte-transparent: output to the client is the exact bytes the server
    wrote. A frame that fails to parse as JSON is still forwarded verbatim
    (and logged raw) — the shim never gates or repairs traffic.
  * Fail-open: no --log and no $SENSE_IO_LOG → exec the server directly
    (zero interposition). A log write error disables logging and lets the
    session continue; capture is telemetry, never a run dependency.
  * Bench-only: nothing in `sense` references this file. It exists in a
    run only when the runner rewrites the MCP command to name it.
"""

import argparse
import datetime
import json
import os
import signal
import subprocess
import sys
import threading


def parse_args(argv):
    ap = argparse.ArgumentParser(add_help=True)
    ap.add_argument("--log", default=os.environ.get("SENSE_IO_LOG") or None,
                    help="JSONL capture file (default: $SENSE_IO_LOG; absent = pure passthrough)")
    if "--" not in argv:
        ap.error("server command required after `--`")
    split = argv.index("--")
    opts = ap.parse_args(argv[:split])
    cmd = argv[split + 1:]
    if not cmd:
        ap.error("server command required after `--`")
    return opts, cmd


class FrameLog:
    """Append-only JSONL sink; a write failure silences logging, never the session."""

    def __init__(self, path):
        self._lock = threading.Lock()
        self._fh = None
        try:
            os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
            self._fh = open(path, "a", encoding="utf-8")
        except OSError as e:
            print(f"[mcp_tee] capture disabled (cannot open {path}: {e})", file=sys.stderr)

    def record(self, direction, raw_line):
        if self._fh is None:
            return
        text = raw_line.decode("utf-8", errors="replace").rstrip("\r\n")
        if not text.strip():
            return
        try:
            msg = json.loads(text)
        except ValueError:
            msg = {"_unparsed": text}
        entry = {
            "ts": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "dir": direction,
            "msg": msg,
        }
        try:
            with self._lock:
                self._fh.write(json.dumps(entry, ensure_ascii=False) + "\n")
                self._fh.flush()
        except OSError as e:
            print(f"[mcp_tee] capture disabled mid-session ({e})", file=sys.stderr)
            self._fh = None


def pump(src, dst, log, direction):
    """Forward src → dst line-by-line, logging each frame. Bytes pass unchanged."""
    try:
        for line in iter(src.readline, b""):
            dst.write(line)
            dst.flush()
            log.record(direction, line)
    except (BrokenPipeError, ValueError, OSError):
        pass
    finally:
        try:
            dst.close()
        except OSError:
            pass


def main(argv):
    opts, cmd = parse_args(argv)

    if not opts.log:
        os.execvp(cmd[0], cmd)  # pure passthrough; the shim vanishes

    child = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE)
    log = FrameLog(opts.log)

    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda s, _f: child.send_signal(s))

    stdin = sys.stdin.buffer
    stdout = sys.stdout.buffer
    t_in = threading.Thread(target=pump, args=(stdin, child.stdin, log, "c2s"), daemon=True)
    t_out = threading.Thread(target=pump, args=(child.stdout, stdout, log, "s2c"))
    t_in.start()
    t_out.start()
    t_out.join()  # server closed stdout → session over
    return child.wait()


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
