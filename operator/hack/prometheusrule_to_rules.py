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


def main() -> int:
    args = sys.argv[1:]
    if args and args[0] in ("-h", "--help"):
        if len(args) != 1:
            print(usage(), file=sys.stderr)
            print("error: --help takes no arguments", file=sys.stderr)
            return 2
        print(__doc__.strip())
        print()
        print(usage())
        return 0
    if args and args[0].startswith("-"):
        print(usage(), file=sys.stderr)
        print(f"error: unknown option: {args[0]}", file=sys.stderr)
        return 2
    if len(args) != 2:
        print(usage(), file=sys.stderr)
        print(f"error: expected 2 arguments, got {len(args)}", file=sys.stderr)
        return 2
    src, dst = Path(args[0]), Path(args[1])
    try:
        # encoding= so LC_ALL=C (Makefile) does not decode as ASCII.
        # write_bytes keeps LF on platforms whose text mode would emit CRLF.
        body = extract_groups(src.read_text(encoding="utf-8"))
        dst.write_bytes(body.encode("utf-8"))
    except (OSError, ValueError) as e:
        print(e, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
