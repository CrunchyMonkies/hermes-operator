#!/usr/bin/env python3
"""Copy controller-gen CRDs into the Helm chart, injecting the
`helm.sh/resource-policy: keep` annotation on each CRD's top-level metadata so
`helm uninstall` never orphans live HermesAgent CRs (spec §9.3a).

Usage: annotate-crds.py <src-dir> <dst-dir>
"""
import glob
import os
import sys


def annotate(src_path: str, dst_path: str) -> None:
    with open(src_path) as fh:
        lines = fh.readlines()
    out, inserted = [], False
    for line in lines:
        out.append(line)
        if not inserted and line.rstrip() == "metadata:":
            out.append("  annotations:\n")
            out.append('    "helm.sh/resource-policy": keep\n')
            inserted = True
    with open(dst_path, "w") as fh:
        fh.writelines(out)


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    src_dir, dst_dir = sys.argv[1], sys.argv[2]
    os.makedirs(dst_dir, exist_ok=True)
    for f in sorted(glob.glob(os.path.join(src_dir, "*.yaml"))):
        dst = os.path.join(dst_dir, os.path.basename(f))
        annotate(f, dst)
        print(f"annotated {dst}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
