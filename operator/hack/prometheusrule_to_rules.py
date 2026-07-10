#!/usr/bin/env python3
"""Extract a PrometheusRule CR's spec.groups into a plain Prometheus rules file
so promtool can unit-test the alerts (promtool wants `groups:`, not the CR wrapper).

Usage: prometheusrule_to_rules.py <prometheusrule.yaml> <out-rules.yaml>
"""
import sys

import yaml


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    with open(sys.argv[1]) as f:
        cr = yaml.safe_load(f)
    groups = cr.get("spec", {}).get("groups")
    if not groups:
        print("no spec.groups in PrometheusRule", file=sys.stderr)
        return 1
    with open(sys.argv[2], "w") as f:
        yaml.safe_dump({"groups": groups}, f, default_flow_style=False, sort_keys=False)
    return 0


if __name__ == "__main__":
    sys.exit(main())
