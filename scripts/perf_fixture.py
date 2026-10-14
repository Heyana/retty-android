#!/usr/bin/env python3
"""Deterministic PTY workload; run only by performance-baseline.py."""

import os
import select
import sys
import termios
import time
import tty


def history(rows):
    return b"".join(
        f"\x1b[32m{i:06d}\x1b[0m performance-needle 中文 terminal baseline\r\n".encode()
        for i in range(rows)
    )


def write_all(data):
    remaining = memoryview(data)
    while remaining:
        written = os.write(1, remaining)
        if written <= 0:
            raise OSError("fixture output stopped")
        remaining = remaining[written:]


def next_deadline(previous, now, interval):
    # Preserve the requested average rate after delayed wakeups, with at most
    # four chunks of catch-up rather than an unbounded burst.
    return max(previous + interval, now - 3 * interval)


def main():
    fd = sys.stdin.fileno()
    original = termios.tcgetattr(fd)
    tty.setraw(fd)
    pending = b""
    rate = 0
    deadline = time.monotonic()
    row = b"\x1b[34moutput\x1b[0m performance-needle 0123456789abcdef\r\n"
    chunk = row * (65536 // len(row))
    try:
        os.write(1, b"\x1b[?25lREADY\r\n")
        while True:
            timeout = max(0, deadline - time.monotonic()) if rate else None
            readable, _, _ = select.select([fd], [], [], timeout)
            if readable:
                data = os.read(fd, 4096)
                if not data:
                    return
                pending += data
                while b"\n" in pending:
                    line, pending = pending.split(b"\n", 1)
                    command, _, value = line.decode().partition(" ")
                    if command == "ECHO":
                        os.write(1, f"PERF_ECHO_{value}\r\n".encode())
                    elif command == "HISTORY":
                        write_all(history(int(value)))
                        os.write(1, b"HISTORY_DONE\r\n")
                    elif command == "OUTPUT":
                        rate = int(value)
                        deadline = time.monotonic()
                        os.write(1, b"OUTPUT_STARTED\r\n")
                    elif command == "STOP":
                        rate = 0
                        os.write(1, b"OUTPUT_STOPPED\r\n")
                    elif command == "QUIT":
                        return
                    else:
                        raise ValueError("unknown fixture command")
            if rate and time.monotonic() >= deadline:
                write_all(chunk)
                deadline = next_deadline(deadline, time.monotonic(), len(chunk) / rate)
    finally:
        termios.tcsetattr(fd, termios.TCSANOW, original)


if __name__ == "__main__":
    main()
