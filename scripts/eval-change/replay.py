#!/usr/bin/env python3
"""Replay one TypeScript change and score the done check against `tsc`.

A throwaway clone is put at the parent of a commit and indexed there. Then only the
named files are written from the commit, the way an agent has edited the model and not
yet its users. `ckb changes` answers, and `npx tsc --noEmit` before and after is the
truth: every error line that is new is a use the change broke.

    replay.py --ckb BIN --repo APP --parent SHA --commit SHA --work DIR FILE...
    replay.py ... --expect-clear      # a change that breaks nothing: exit 1 unless clear

The work dir keeps the clone, both tsc outputs (they don't depend on the ckb binary and
are reused) and one result per run. It holds symbol and file names of the app: for a
private app, keep it outside this repo and report aggregates only. Python 3 stdlib.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

TSC_LINE = re.compile(r"^(?P<file>[^\s(][^(]*)\((?P<line>\d+),(?P<col>\d+)\): error (?P<code>TS\d+): (?P<msg>.*)$")
NEAR = 3


def sh(cmd, cwd, env=None, timeout=1800):
    start = time.time()
    p = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    return p.returncode, p.stdout, p.stderr, time.time() - start


def git(repo, *args):
    return subprocess.run(["git", "-C", str(repo), *args], capture_output=True, text=True, check=True).stdout


def tsc_errors(text):
    """One entry per error line: (file, line, code, message)."""
    found = set()
    for line in text.splitlines():
        m = TSC_LINE.match(line)
        if m:
            found.add((m["file"], int(m["line"]), m["code"], m["msg"]))
    return found


def tsc(clone, cache, refresh):
    if cache.exists() and not refresh:
        return cache.read_text()
    _, out, err, _ = sh(["npx", "tsc", "--noEmit", "--pretty", "false"], clone)
    cache.write_text(out + err)
    return out + err


def reset(clone, parent):
    git(clone, "checkout", "-qf", parent)
    # Not -x: the linked node_modules and the index stay.
    git(clone, "clean", "-qfd", "-e", "node_modules", "-e", ".ckb", "-e", "index.scip")


def apply(clone, commit, files):
    for path in files:
        blob = subprocess.run(["git", "-C", str(clone), "show", f"{commit}:{path}"], capture_output=True, check=True).stdout
        target = clone / path
        new = not target.exists()
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(blob)
        if new:
            subprocess.run(["git", "-C", str(clone), "add", "-N", "-f", path], capture_output=True)


def score(errors, sites, applied, top):
    """Error lines against open sites. Errors inside the applied files are the change's
    own and no use site, so they are counted apart."""
    own = {e for e in errors if e[0] in applied}
    # One entry per position: two diagnostics on one line are one place to fix.
    uses = sorted({(e[0], e[1]) for e in errors - own})
    by_file = {}
    for s in sites:
        by_file.setdefault(s.get("file"), set()).add(s.get("line"))
    exact = [e for e in uses if e[1] in by_file.get(e[0], ())]
    near = [e for e in uses if any(abs(e[1] - l) <= NEAR for l in by_file.get(e[0], ()))]
    missed = [e for e in uses if e not in near]
    error_lines = set(uses)
    error_files = {e[0] for e in uses}
    site_files = {s.get("file") for s in sites}
    on_error = [s for s in sites if (s.get("file"), s.get("line")) in error_lines]
    first = sites[:top]
    return {
        "tsc_new_error_lines": len(uses),
        "tsc_new_error_files": len(error_files),
        "tsc_errors_in_applied_files": len(own),
        "open_sites": len(sites),
        "open_site_files": len(site_files),
        "error_lines_exact": len(exact),
        "error_lines_near": len(near),
        "error_lines_missed": len(missed),
        "error_files_not_named": len(error_files - site_files),
        "sites_on_error_line": len(on_error),
        "sites_not_flagged_by_tsc": len(sites) - len(on_error),
        f"first_{top}_on_error_line": sum(1 for s in first if (s.get("file"), s.get("line")) in error_lines),
    }, {"missed": missed, "files_not_named": sorted(error_files - site_files)}


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--ckb", required=True)
    ap.add_argument("--repo", required=True, type=Path, help="the app's checkout; only cloned and read")
    ap.add_argument("--parent", required=True, help="commit the index is built at")
    ap.add_argument("--commit", required=True, help="commit the files are taken from")
    ap.add_argument("--work", required=True, type=Path, help="work dir, outside any repo you care about")
    ap.add_argument("--name", default="run", help="label for this run's result files")
    ap.add_argument("--lang", default="ts")
    ap.add_argument("--top", type=int, default=20, help="how many leading sites to score for ranking")
    ap.add_argument("--reuse-index", action="store_true", help="keep the index of an earlier run")
    ap.add_argument("--refresh-tsc", action="store_true", help="run tsc again instead of reading the cached output")
    ap.add_argument("--expect-clear", action="store_true", help="exit 1 unless the done check is clear")
    ap.add_argument("--show", action="store_true", help="print the missed error lines (names of the app)")
    ap.add_argument("files", nargs="+")
    args = ap.parse_args()

    ckb = str(Path(args.ckb).resolve())
    src = args.repo.resolve()
    work = args.work.resolve()
    work.mkdir(parents=True, exist_ok=True)
    parent = git(src, "rev-parse", "--short=10", args.parent).strip()
    commit = git(src, "rev-parse", "--short=10", args.commit).strip()
    case = f"{parent}-{commit}"
    clone = work / "clone"
    env = dict(os.environ, CKB_HOME=str(work / "ckb-home"), CKB_NO_UPDATE_CHECK="1")

    if not clone.exists():
        subprocess.run(["git", "clone", "-q", str(src), str(clone)], check=True)
    else:
        subprocess.run(["git", "-C", str(clone), "fetch", "-q", "origin"], check=False)
    if (src / "node_modules").exists() and not (clone / "node_modules").exists():
        (clone / "node_modules").symlink_to(src / "node_modules")

    # One heavy job at a time: tsc, then the index, then tsc again.
    reset(clone, parent)
    before = tsc_errors(tsc(clone, work / f"tsc-{parent}.txt", args.refresh_tsc))

    marker = work / "indexed-at"
    indexed = marker.read_text().strip() if marker.exists() else ""
    index_seconds = None
    if not (args.reuse_index and indexed == parent):
        sh([ckb, "init", "--force"], clone, env, 120)
        rc, _, err, index_seconds = sh([ckb, "index", "--force", "--lang", args.lang], clone, env)
        if rc != 0:
            sys.exit("index failed: " + err[-800:])
        marker.write_text(parent)

    apply(clone, commit, args.files)
    key = "+".join(sorted(Path(f).name for f in args.files))
    after = tsc_errors(tsc(clone, work / f"tsc-{case}-{key}.txt", args.refresh_tsc))

    rc, out, err, changes_seconds = sh([ckb, "changes", "--format", "json"], clone, env, 1200)
    if rc != 0:
        sys.exit("ckb changes failed: " + err[-800:])
    full = json.loads(out[out.find("{"):])
    _, compact, _, _ = sh([ckb, "changes", "--format", "compact"], clone, env, 1200)
    (work / f"{args.name}-{case}.json").write_text(out)
    (work / f"{args.name}-{case}.compact.txt").write_text(compact)

    done = full.get("doneCheck") or {}
    sites = done.get("openSites") or []
    summary, detail = score(after - before, sites, set(args.files), args.top)
    changed = full.get("changedSymbols") or []
    summary.update({
        "case": case,
        "done_status": done.get("status"),
        "changed_symbols": len(changed),
        "changed_file_level": sum(1 for c in changed if str(c.get("symbolId", "")).startswith("file:")),
        "compact_bytes": len(compact.encode()),
        "json_bytes": len(out.encode()),
        "index_seconds": None if index_seconds is None else round(index_seconds),
        "changes_seconds": round(changes_seconds, 1),
    })
    (work / f"{args.name}-{case}.summary.json").write_text(json.dumps({"summary": summary, "detail": detail}, indent=1))
    print(json.dumps(summary, indent=1))
    if args.show:
        messages = {}
        for e in after - before:
            messages.setdefault((e[0], e[1]), f"{e[2]} {e[3][:110]}")
        for e in detail["missed"]:
            print(f"missed {e[0]}:{e[1]} {messages.get((e[0], e[1]), '')}")
        for f in detail["files_not_named"]:
            print("not named", f)
    reset(clone, parent)
    if args.expect_clear and done.get("status") != "clear":
        sys.exit(1)


if __name__ == "__main__":
    main()
