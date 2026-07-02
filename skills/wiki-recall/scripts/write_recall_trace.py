#!/usr/bin/env python3
"""Compatibility wrapper for memex_tools.py write-trace."""

from __future__ import annotations

import sys
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parent))


def main() -> int:
    from memex_tools import main as tools_main

    return tools_main(["write-trace", *sys.argv[1:]])


if __name__ == "__main__":
    raise SystemExit(main())
