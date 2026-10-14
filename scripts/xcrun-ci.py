#!/usr/bin/env python3
"""Retry transient Apple ticket transport failures without weakening release checks."""

import os
import re
import subprocess
import sys
import time


XCRUN = "/usr/bin/xcrun"
MAX_ATTEMPTS = 5
NETWORK_ERROR = re.compile(r"NSURLErrorDomain Code=-(?:1001|1003|1004|1005|1009)\b")


def run(args):
    command = [XCRUN, *args]
    if len(args) < 2 or args[:2] not in (["stapler", "staple"], ["stapler", "validate"]):
        os.execv(XCRUN, command)
        return 0

    for attempt in range(1, MAX_ATTEMPTS + 1):
        result = subprocess.run(
            command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            encoding="utf-8", errors="replace", check=False,
        )
        print(result.stdout, end="", flush=True)
        if result.returncode != 68 or not NETWORK_ERROR.search(result.stdout):
            return result.returncode
        if attempt == MAX_ATTEMPTS:
            return result.returncode
        delay = 2 ** attempt
        print(
            f"::warning::Apple stapler network failure (attempt {attempt}/{MAX_ATTEMPTS}); "
            f"retrying in {delay}s.", flush=True,
        )
        time.sleep(delay)


if __name__ == "__main__":
    sys.exit(run(sys.argv[1:]))
