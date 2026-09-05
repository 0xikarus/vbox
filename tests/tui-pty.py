#!/usr/bin/env python3
"""Bounded real-PTY test; pass a `go test -c ./internal/cli` binary."""
import fcntl
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time


def run(keys, expected, task=False):
    master, slave = pty.openpty()
    before = termios.tcgetattr(slave)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
    env = dict(os.environ, VMBOX_TUI_PTY_TEST="1", TERM="xterm-256color")
    if task:
        env["VMBOX_TUI_PTY_TASK"] = "1"
    child = subprocess.Popen([sys.argv[1], "-test.run", "^TestTUIRealTerminalHelper$"],
                             stdin=slave, stdout=slave, stderr=slave, env=env)
    output = b""

    def until(marker):
        nonlocal output
        deadline = time.monotonic() + 5
        while marker not in output:
            assert time.monotonic() < deadline, ("PTY timeout", output)
            if select.select([master], [], [], 0.1)[0]:
                output += os.read(master, 65536)

    try:
        until(b"Choose an agent" if task else b"Real terminal sessions")
        assert not termios.tcgetattr(slave)[3] & termios.ICANON
        # Resize while the picker waits for real terminal input.
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 10, 40, 0, 0))
        until(b"\x1b[10;1H")
        os.write(master, keys)
        until(expected)
        assert child.wait(timeout=5) == 0
        assert termios.tcgetattr(slave) == before, "terminal mode not restored"
        assert b"\x1b[?1049h" in output and b"\x1b[?1049l" in output
        assert b"\x1b[?25h" in output
    finally:
        if child.poll() is None:
            child.kill()
            child.wait()
        os.close(master)
        os.close(slave)


run(b"\x1b[B\x1b[B\x1b[A\r", b"RESULT=1 ERROR=<nil>")
run(b"\x1b[F\r", b"RESULT=39 ERROR=<nil>")
run(b"\x1b", b"RESULT=-1 ERROR=selection cancelled")
run(b"\r", b"AGENT=codex ERROR=<nil>", task=True)
run(b"\x1b[B\r", b"AGENT=claude ERROR=<nil>", task=True)
run(b"\x1b[A\r", b"AGENT=shell ERROR=<nil>", task=True)
run(b"\x1b", b"AGENT= ERROR=selection cancelled", task=True)
print("PASS: real PTY arrows/Enter, long-list navigation, resize, Escape, terminal restoration")
print("PASS: one-shot agent selector Codex/Claude/shell and cancellation in real PTY")
