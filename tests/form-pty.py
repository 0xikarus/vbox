#!/usr/bin/env python3
"""Bounded unified-form interaction against a real PTY and compiled CLI tests."""
import fcntl
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time


def run(cancel=False):
    master, slave = pty.openpty()
    before = termios.tcgetattr(slave)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
    child = subprocess.Popen(
        [sys.argv[1], "-test.run", "^TestFormRealTerminalHelper$"],
        stdin=slave, stdout=slave, stderr=slave,
        env=dict(os.environ, VMBOX_FORM_PTY_TEST="1", TERM="xterm-256color"))
    output = b""

    def until(marker):
        nonlocal output
        deadline = time.monotonic() + 5
        while marker not in output:
            assert time.monotonic() < deadline, ("form timeout", output)
            if select.select([master], [], [], 0.1)[0]:
                output += os.read(master, 65536)

    try:
        until(b"Creation form PTY")
        assert not termios.tcgetattr(slave)[3] & termios.ICANON
        # Bracketed paste must populate the field, not submit the form.
        os.write(master, "\r\x15\x1b[200~Grüße\x1b[201~\r\t\x1b[C".encode())
        until("Grüße".encode())
        until(b"Hibernate")
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 10, 40, 0, 0))
        if cancel:
            os.write(master, b"\x03")
            until("FORM name=Grüße mode=Hibernate calls=0".encode())
        else:
            os.write(master, b"\t\r")
            until(b"temporary failure; retry")
            os.write(master, b"\r")
            until("FORM name=Grüße mode=Hibernate calls=2 error=<nil>".encode())
        assert child.wait(timeout=5) == 0
        assert termios.tcgetattr(slave) == before, "terminal mode not restored"
        assert output.count(b"\x1b[?1049h") == 1, "form opened multiple screens"
        assert output.count(b"\x1b[?1049l") == 1, "form did not restore screen"
    finally:
        if child.poll() is None:
            child.kill()
            child.wait()
        os.close(master)
        os.close(slave)


run()
run(cancel=True)
print("PASS: real PTY form Unicode paste, inline options, retry, cancel, single screen and terminal restoration")
