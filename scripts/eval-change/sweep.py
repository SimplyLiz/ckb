#!/usr/bin/env python3
"""Count the done check's false alarms by replaying commits that compile.

For each commit: check that it builds (and, for Go, vets), index its parent, put the
whole commit into the working tree, run `ckb changes`. A commit that builds leaves no
use broken, so every open site it gets is a false alarm, and every `not_assessed` note
names a doubt worth reading.

    sweep.py --ckb BIN --repo SCRATCH_CLONE --out results.jsonl [--lang go] SHA...
    sweep.py --report results.jsonl

The repo is checked out with -f and cleaned with -fdx for every commit: give it a
clone made for this, never a checkout you work in. Results are appended, and commits
already in --out are skipped, so a run can be resumed. Python 3 stdlib only.
"""

import argparse
import collections
import json
import os
import subprocess
import sys
import time
from pathlib import Path

BUILD = {
    "go": "go build ./... && go vet ./...",
    "rust": "cargo check -q --all-targets",
    "ts": "npx tsc --noEmit",
}


def git(repo, *args, check=True):
    return subprocess.run(["git", "-C", str(repo), *args], capture_output=True, text=True, check=check).stdout


def toplevel(path):
    out = subprocess.run(["git", "-C", str(path), "rev-parse", "--show-toplevel"], capture_output=True, text=True)
    return Path(out.stdout.strip()).resolve() if out.returncode == 0 else None


def unskip(repo):
    for line in git(repo, "ls-files", "-v").splitlines():
        if line.startswith("S "):
            git(repo, "update-index", "--no-skip-worktree", line[2:])


def sweep_one(args, env, sha):
    repo = args.repo
    rec = {"sha": sha}

    def run(cmd, timeout=1800):
        start = time.time()
        p = subprocess.run(cmd, cwd=repo, env=env, capture_output=True, text=True, timeout=timeout)
        return p.returncode, p.stdout, p.stderr, time.time() - start

    unskip(repo)
    parent = git(repo, "rev-parse", sha + "^").strip()
    git(repo, "checkout", "-qf", sha)
    git(repo, "clean", "-qfdx")
    rc, _, err, _ = run(["sh", "-c", BUILD[args.lang]])
    rec["builds"] = rc == 0
    if rc != 0:
        rec["build_err"] = err[-500:]

    git(repo, "checkout", "-qf", parent)
    git(repo, "clean", "-qfdx")
    run([args.ckb, "init", "--force"], 120)
    rc, _, err, secs = run([args.ckb, "index", "--force", "--lang", args.lang])
    rec["index_seconds"] = round(secs)
    if rc != 0:
        rec["status"], rec["err"] = "index-failed", err[-500:]
        return rec
    # The index files are tracked in some repos; the commit must not overwrite them.
    for path in git(repo, "ls-files", ".ckb", ".scip", "index.scip").split():
        git(repo, "update-index", "--skip-worktree", path)

    changed = []
    for line in git(repo, "diff", "--no-renames", "--name-status", parent, sha).splitlines():
        status, path = line.split("\t", 1)
        changed.append(path)
        target = repo / path
        if status == "D":
            target.unlink(missing_ok=True)
            continue
        blob = subprocess.run(["git", "-C", str(repo), "show", f"{sha}:{path}"], capture_output=True, check=True).stdout
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(blob)
        if status == "A":  # So `git diff HEAD` shows it; -f for files the repo ignores
            git(repo, "add", "-N", "-f", path, check=False)
    rec["changed_files"] = changed

    rc, out, err, _ = run([args.ckb, "changes", "--format", "json"], 1200)
    if rc != 0:
        rec["status"], rec["err"] = "changes-failed", err[-500:]
    else:
        done = json.loads(out[out.find("{"):]).get("doneCheck") or {}
        rec["status"] = done.get("status")
        rec["open"] = done.get("openSites") or []
        rec["open_count"] = done.get("openCount", 0)
        rec["note"] = done.get("note")
    unskip(repo)
    return rec


def report(path):
    recs = [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]
    built = [r for r in recs if r.get("builds")]
    statuses = collections.Counter(r.get("status") for r in built)
    print(f"{len(recs)} commits, {len(built)} build")
    print("status: " + ", ".join(f"{k} {v}" for k, v in sorted(statuses.items(), key=lambda kv: str(kv[0]))))
    print(f"open sites in commits that build (false alarms): {sum(r.get('open_count', 0) for r in built)}")
    for r in built:
        if r.get("status") == "open":
            for site in r["open"]:
                print(f"  {r['sha']} {site.get('file')}:{site.get('line')} {site.get('symbol')} {site.get('change')}")
        elif r.get("status") == "not_assessed":
            note = r.get("note") or ""
            print(f"  {r['sha']} not_assessed: {note[note.find('but ') + 4:] if 'but ' in note else note}")


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--ckb", help="ckb binary to measure")
    ap.add_argument("--repo", type=Path, help="a scratch clone: it is reset for every commit")
    ap.add_argument("--out", help="results, one JSON line per commit")
    ap.add_argument("--lang", default="go", choices=sorted(BUILD))
    ap.add_argument("--report", metavar="RESULTS", help="summarize a results file and exit")
    ap.add_argument("shas", nargs="*")
    args = ap.parse_args()

    if args.report:
        report(args.report)
        return
    if not (args.ckb and args.repo and args.out and args.shas):
        ap.error("--ckb, --repo, --out and at least one SHA are needed")
    args.repo = args.repo.resolve()
    args.ckb = str(Path(args.ckb).resolve())
    if toplevel(args.repo) is None or toplevel(args.repo) == toplevel(Path(__file__).parent):
        sys.exit("--repo has to be a separate clone: this script resets it with checkout -f and clean -fdx")

    env = dict(os.environ, CKB_HOME=str(args.repo.parent / (args.repo.name + "-ckb-home")), CKB_NO_UPDATE_CHECK="1")
    out = Path(args.out)
    done = set()
    if out.exists():
        done = {json.loads(line)["sha"] for line in out.read_text().splitlines() if line.strip()}
    for sha in args.shas:
        if sha in done:
            continue
        rec = sweep_one(args, env, sha)
        with out.open("a") as f:
            f.write(json.dumps(rec) + "\n")
        print(sha, rec.get("builds"), rec.get("status"), rec.get("open_count"), flush=True)


if __name__ == "__main__":
    main()
