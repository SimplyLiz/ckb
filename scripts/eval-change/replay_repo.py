#!/usr/bin/env python3
"""Run replay.py over a repo's sample from select_replay.py, in order, until --keep
commits with a clean parent are measured.

    replay_repo.py --ckb BIN --sample sample.json --work DIR [--keep 5] [--check CMD] [--check-dir D]

A candidate whose parent the compiler rejects is skipped before anything of CKB runs, so
the skips can't depend on CKB's answer. One replay at a time: each runs the compiler, the
index and the compiler again. Results land in DIR as replay.py writes them; `runs.jsonl`
lists every candidate tried with its outcome. Python 3 stdlib.
"""

import argparse
import json
import subprocess
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--ckb", required=True)
    ap.add_argument("--sample", required=True, type=Path)
    ap.add_argument("--work", required=True, type=Path)
    ap.add_argument("--keep", type=int, default=5)
    ap.add_argument("--check")
    ap.add_argument("--check-dir", default="")
    ap.add_argument("--name", default="frozen")
    ap.add_argument("--timeout", type=int, default=3600)
    ap.add_argument("--undo-compiler-writes", action="store_true")
    a = ap.parse_args()

    sample = json.loads(a.sample.read_text())
    a.work.mkdir(parents=True, exist_ok=True)
    runs = a.work / "runs.jsonl"
    done = {}
    if runs.exists():
        for line in runs.read_text().splitlines():
            r = json.loads(line)
            done[r["sha"]] = r
    kept = sum(1 for r in done.values() if r["outcome"] == "measured")
    for c in sample["sample"]:
        if kept >= a.keep:
            break
        if c["sha"] in done:
            continue
        cmd = [sys.executable, str(HERE / "replay.py"), "--ckb", a.ckb, "--repo", sample["repo"],
               "--parent", c["parent"], "--commit", c["sha"], "--work", str(a.work), "--lang", sample["lang"],
               "--name", a.name, "--top", "20", "--require-clean-parent"]
        if a.check:
            cmd += ["--check", a.check]
        if a.undo_compiler_writes:
            cmd.append("--undo-compiler-writes")
        if a.check_dir:
            cmd += ["--check-dir", a.check_dir]
        cmd += c["files"]
        start = time.time()
        try:
            p = subprocess.run(cmd, capture_output=True, text=True, timeout=a.timeout)
            rc, out, err = p.returncode, p.stdout, p.stderr
        except subprocess.TimeoutExpired:
            rc, out, err = -1, "", "timeout"
        outcome = {0: "measured", 3: "parent-broken"}.get(rc, "failed")
        rec = {"sha": c["sha"], "parent": c["parent"], "outcome": outcome, "rc": rc,
               "seconds": round(time.time() - start), "files": len(c["files"])}
        if outcome == "measured":
            rec["summary"] = json.loads(out[out.find("{"):])
            kept += 1
        else:
            rec["tail"] = (out + err)[-1500:]
        with runs.open("a") as f:
            f.write(json.dumps(rec) + "\n")
        print(f"{c['sha'][:10]} {outcome} ({rec['seconds']}s), kept {kept}/{a.keep}", flush=True)
    print(f"done: {kept} measured")


if __name__ == "__main__":
    main()
