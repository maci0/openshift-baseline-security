#!/usr/bin/env python3
"""Extract a PrometheusRule CR's spec.groups into a plain Prometheus rules file
so promtool can unit-test the alerts (promtool wants `groups:`, not the CR wrapper).

Stdlib only: no PyYAML. The checked-in PrometheusRule uses 2-space indent and
puts `groups:` as a direct child of `spec:`. We slice that block and dedent by
two spaces so the output starts with top-level `groups:`.

Usage: prometheusrule_to_rules.py <prometheusrule.yaml> <out-rules.yaml>
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


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    src, dst = Path(sys.argv[1]), Path(sys.argv[2])
    try:
        body = extract_groups(src.read_text())
        dst.write_text(body)
    except (OSError, ValueError) as e:
        print(e, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
