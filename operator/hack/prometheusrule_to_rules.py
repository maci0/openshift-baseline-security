#!/usr/bin/env python3
"""Extract a PrometheusRule CR's spec.groups into a plain Prometheus rules file
so promtool can unit-test the alerts (promtool wants `groups:`, not the CR wrapper).

Stdlib only: no PyYAML. The checked-in PrometheusRule uses 2-space indent and
puts `groups:` as a direct child of `spec:`. We slice that block and dedent by
two spaces so the output starts with top-level `groups:`.
"""

from __future__ import annotations

import sys
from pathlib import Path
from typing import NoReturn


def extract_groups(text: str) -> str:
    lines = text.splitlines(keepends=True)
    start = None
    for i, line in enumerate(lines):
        # Direct child of spec: (exactly two leading spaces).
        if line.startswith("  groups:"):
            start = i
            break
    if start is None:
        raise ValueError("no spec.groups block found (expected a '  groups:' line)")

    out: list[str] = []
    for line in lines[start:]:
        if line.startswith("  "):
            out.append(line[2:])
            continue
        if not line.strip():
            out.append(line if line.endswith("\n") else f"{line}\n")
            continue
        # A non-indented, non-empty line ends the block (next top-level key).
        break
    if not out:
        raise ValueError("empty groups block")
    return "".join(out)


def usage() -> str:
    return f"Usage: {Path(sys.argv[0]).name} <prometheusrule.yaml> <out-rules.yaml>"


def fail(message: str) -> None:
    """Report a diagnostic on stderr under this script's name, as hack/*.sh do."""
    print(f"{Path(sys.argv[0]).name}: {message}", file=sys.stderr)


def usage_error(message: str) -> NoReturn:
    """Report a bad invocation and exit 2: the diagnostic first, then the
    usage, on stderr, matching hack/*.sh so a caller piping stdout sees
    nothing on the error path."""
    fail(message)
    print(usage(), file=sys.stderr)
    raise SystemExit(2)


def main() -> int:
    args = sys.argv[1:]
    if args and args[0] in ("-h", "--help"):
        if len(args) != 1:
            usage_error("--help takes no arguments")
        print(__doc__.strip())
        print()
        print(usage())
        return 0
    if args and args[0].startswith("-"):
        usage_error(f"unknown option: {args[0]}")
    if len(args) != 2:
        usage_error(f"expected 2 arguments, got {len(args)}")
    src, dst = Path(args[0]), Path(args[1])
    try:
        # encoding= so LC_ALL=C (Makefile) does not decode as ASCII.
        # write_bytes keeps LF on platforms whose text mode would emit CRLF.
        body = extract_groups(src.read_text(encoding="utf-8"))
        dst.write_bytes(body.encode("utf-8"))
    except (OSError, ValueError) as e:
        fail(str(e))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
