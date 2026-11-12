#!/usr/bin/env python3
"""Replay one change and score the done check against the compiler.

A throwaway clone is put at the parent of a commit and indexed there. Then only the
named files are written from the commit, the way an agent has edited the model and not
yet its users. `ckb changes` answers, and the compiler before and after is the truth:
every error line that is new is a use the change broke. TypeScript runs
`npx tsc --noEmit`, Rust `cargo check`; `--check` sets another command.

    replay.py --ckb BIN --repo APP --parent SHA --commit SHA --work DIR FILE...
    replay.py ... --expect-clear      # a change that breaks nothing: exit 1 unless clear
    replay.py --lang rust --check-dir engine ...   # a manifest below the root

Rust truth is a lower bound: cargo checks no crate that depends on one with errors.

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
# cargo --message-format=short: `src/a.rs:3:5: error[E0061]: this function takes …`
CARGO_LINE = re.compile(r"^(?P<file>[^\s:][^:]*\.rs):(?P<line>\d+):(?P<col>\d+): error(?:\[(?P<code>E\d+)\])?: (?P<msg>.*)$")
CHECKS = {
    "ts": ["npx", "tsc", "--noEmit", "--pretty", "false"],
    "rust": ["cargo", "check", "--workspace", "--all-targets", "--keep-going", "--message-format=short"],
}
NEAR = 3


def sh(cmd, cwd, env=None, timeout=1800):
    start = time.time()
    p = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    return p.returncode, p.stdout, p.stderr, time.time() - start


def git(repo, *args):
    return subprocess.run(["git", "-C", str(repo), *args], capture_output=True, text=True, check=True).stdout


def compiler_errors(text, prefix=""):
    """One entry per error line: (file, line, code, message), the file relative to the
    clone. `prefix` is the directory the compiler ran in."""
    found = set()
    for line in text.splitlines():
        m = TSC_LINE.match(line) or CARGO_LINE.match(line)
        if m:
            path = os.path.normpath(os.path.join(prefix, m["file"]))
            found.add((path, int(m["line"]), m["code"] or "error", m["msg"]))
    return found


def compiler(clone, cmd, prefix, cache, refresh, env):
    """The compiler's output, cached: it doesn't depend on the ckb binary."""
    if cache.exists() and not refresh:
        return cache.read_text()
    _, out, err, seconds = sh(cmd, clone / prefix, env)
    cache.write_text(out + err)
    (cache.parent / (cache.name + ".seconds")).write_text(str(round(seconds)))
    return out + err


def link_node_modules(src, clone):
    """Link every node_modules of the checkout up to three levels down, so a workspace
    package resolves its own dependencies."""
    for nm in [src / "node_modules", *src.glob("*/node_modules"), *src.glob("*/*/node_modules")]:
        rel = nm.relative_to(src)
        target = clone / rel
        if nm.is_dir() and target.parent.exists() and not target.exists():
            target.symlink_to(nm)


def reset(clone, parent):
    git(clone, "checkout", "-qf", parent)
    # Not -x: the linked node_modules and the index stay.
    git(clone, "clean", "-qfd", "-e", "node_modules", "-e", ".ckb", "-e", "index.scip", "-e", "target")


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
    ap.add_argument("--lang", default="ts", choices=sorted(CHECKS))
    ap.add_argument("--check", help="compiler command, run through the shell (default per --lang)")
    ap.add_argument("--check-dir", default="", help="directory below the root the compiler runs in")
    ap.add_argument("--top", type=int, default=20, help="how many leading sites to score for ranking")
    ap.add_argument("--reuse-index", action="store_true", help="keep the index of an earlier run")
    ap.add_argument("--refresh-tsc", action="store_true", help="run the compiler again instead of reading the cached output")
    ap.add_argument("--expect-clear", action="store_true", help="exit 1 unless the done check is clear")
    ap.add_argument("--require-clean-parent", action="store_true",
                    help="exit 3 before indexing when the compiler reports errors at the parent")
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
    link_node_modules(src, clone)
    cmd = ["sh", "-c", args.check] if args.check else CHECKS[args.lang]
    # Rust builds into the work dir, so a run in the clone never touches the checkout's target.
    cenv = dict(os.environ, CARGO_TARGET_DIR=str(work / "target"))
    label = "tsc" if args.lang == "ts" and not args.check else "check"

    # One heavy job at a time: the compiler, then the index, then the compiler again.
    reset(clone, parent)
    before = compiler_errors(compiler(clone, cmd, args.check_dir, work / f"{label}-{parent}.txt", args.refresh_tsc, cenv), args.check_dir)
    if args.require_clean_parent and before:
        print(json.dumps({"case": case, "skipped": "parent has compiler errors", "parent_errors": len(before)}))
        sys.exit(3)

    marker = work / "indexed-at"
    indexed = marker.read_text().strip() if marker.exists() else ""
    index_seconds = None
    if not (args.reuse_index and indexed == parent):
        sh([ckb, "init", "--force"], clone, env, 120)
        rc, out, err, index_seconds = sh([ckb, "index", "--force", "--lang", args.lang], clone, env)
        (work / f"index-{parent}.log").write_text(f"rc {rc}, {round(index_seconds)}s\n{out}\n{err}")
        if rc != 0:
            sys.exit("index failed: " + err[-800:])
        marker.write_text(parent)

    apply(clone, commit, args.files)
    key = "+".join(sorted(Path(f).name for f in args.files))
    after = compiler_errors(compiler(clone, cmd, args.check_dir, work / f"{label}-{case}-{key}.txt", args.refresh_tsc, cenv), args.check_dir)

    rc, out, err, changes_seconds = sh([ckb, "changes", "--format", "json"], clone, env, 1200)
    if rc != 0:
        sys.exit("ckb changes failed: " + err[-800:])
    full = json.loads(out[out.find("{"):])
    _, compact, _, _ = sh([ckb, "changes", "--format", "compact"], clone, env, 1200)
    (work / f"{args.name}-{case}.json").write_text(out)
    (work / f"{args.name}-{case}.compact.txt").write_text(compact)

    done = full.get("doneCheck") or {}
    # The listed sites are the ones likely to break; the rare ones are counted per
    # symbol and kept apart. Both are scored: what an agent is shown, and everything.
    sites = done.get("openSites") or []
    rarely = done.get("rarelySites") or []
    summary, detail = score(after - before, sites, set(args.files), args.top)
    every, _ = score(after - before, sites + rarely, set(args.files), args.top)
    summary.update({f"with_rarely_{k}": every[k] for k in (
        "open_sites", "error_lines_exact", "error_lines_near", "error_lines_missed", "error_files_not_named")})
    summary["rarely_count"] = done.get("rarelyCount", 0)
    summary["rarely_groups"] = len(done.get("rarely") or [])
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
