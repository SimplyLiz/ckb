#!/usr/bin/env python3
"""Measure whether `ckb changes` predicts the files a signature change breaks.

Three steps, each its own subcommand, so the sample is fixed before any result
exists:

    eval.py select  --repo PATH --lang go|ts|rust --head SHA --out sample.json
    eval.py run     --sample sample.json --ckb BIN --out results.jsonl
    eval.py report  --results results.jsonl [--redact] [--json]

See README.md for what this measures and what it cannot. Python 3 stdlib only.
"""

import argparse
import hashlib
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path

SEED = "ckb-eval-change-v1"
MAX_FILES = 30
SIG_MAX_LINES = 12

# Declaration line -> function name. The signature itself is read from the
# blob, so multi-line parameter lists count.
DECLS = {
    "go": [re.compile(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*[\[(]")],
    "ts": [
        re.compile(r"^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)"),
        re.compile(r"^(?:export\s+)?const\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:<[^>]*>\s*)?\("),
    ],
    "rust": [
        re.compile(
            r"^\s*(?:pub(?:\([^)]*\))?\s+)?(?:default\s+)?(?:const\s+)?(?:async\s+)?"
            r"(?:unsafe\s+)?(?:extern\s+\"[^\"]*\"\s+)?fn\s+([A-Za-z_]\w*)"
        )
    ],
}
EXTS = {"go": (".go",), "ts": (".ts", ".tsx", ".mts", ".cts"), "rust": (".rs",)}
# Names no other file calls.
SKIP_NAMES = {"main", "init"}

GENERATED_RE = re.compile(
    r"(^|/)(vendor|node_modules|third_party|testdata|dist|build|target|generated|\.next|\.claude)/"
    r"|\.pb\.go$|_generated\.go$|_gen\.go$|\.gen\.(go|ts|tsx)$|\.generated\.tsx?$|\.d\.ts$"
)
TEST_RE = {
    "go": re.compile(r"_test\.go$"),
    "ts": re.compile(r"(\.|_)(test|spec)\.[cm]?tsx?$|(^|/)(__tests__|tests?|e2e)/"),
    "rust": re.compile(r"_tests?\.rs$|(^|/)(tests|benches)/"),
}
COMMENT_RE = re.compile(r"^\s*(//|#|\*|/\*)")


def git(repo, *args, check=True):
    r = subprocess.run(["git", "-C", str(repo), *args], capture_output=True)
    if check and r.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)}: {r.stderr.decode(errors='replace')[:400]}")
    return r.stdout.decode("utf-8", errors="replace")


def is_source(path, lang):
    return path.endswith(EXTS[lang]) and not GENERATED_RE.search(path)


def is_test(path, lang):
    return bool(TEST_RE[lang].search(path))


def decl_name(line, lang):
    for rx in DECLS[lang]:
        m = rx.match(line)
        if m:
            return m.group(1)
    return None


def parse_commits(repo, head, limit=None):
    """Yield (sha, parent, {path: status}, {path: (removed, added)}) per non-merge commit."""
    count = [f"-n{limit}"] if limit else []
    proc = subprocess.Popen(
        ["git", "-C", str(repo), "log", *count, "--no-merges", "--no-renames", "-U0", "--no-color",
         "--format=%x00%H %P", "--raw", "-p", head],
        stdout=subprocess.PIPE,
    )
    sha, parent, status, diffs, path = None, None, {}, {}, None
    for raw in proc.stdout:
        line = raw.decode("utf-8", errors="replace").rstrip("\n")
        if line.startswith("\x00"):
            if sha:
                yield sha, parent, status, diffs
            parts = line[1:].split()
            sha = parts[0]
            parent = parts[1] if len(parts) > 1 else None
            status, diffs, path = {}, {}, None
        elif line.startswith(":") and path is None:
            f = line.split("\t")
            if len(f) >= 2:
                status[f[-1]] = f[0].split()[-1][0]
        elif line.startswith("diff --git "):
            path = None
        elif line.startswith("+++ "):
            path = line[6:] if line.startswith("+++ b/") else None
            if path is not None:
                diffs.setdefault(path, ([], []))
        elif path is not None and line.startswith("-") and not line.startswith("--- "):
            diffs[path][0].append(line[1:])
        elif path is not None and line.startswith("+"):
            diffs[path][1].append(line[1:])
    if sha:
        yield sha, parent, status, diffs
    proc.wait()


def has_generated_header(text):
    head = text[:600]
    return "Code generated" in head or "@generated" in head


def signature_at(lines, i):
    """The declaration starting at lines[i], up to its body, without any whitespace."""
    depth, parts = 0, []
    for line in lines[i: i + SIG_MAX_LINES]:
        code = line.split("//")[0]
        stop = False
        for ch in code:
            if ch in "([":
                depth += 1
            elif ch in ")]":
                depth -= 1
            elif depth <= 0 and ch in "{;":
                stop = True
                break
            parts.append(ch)
        if stop:
            break
    sig = re.sub(r"\s+", "", "".join(parts))
    return re.sub(r",([)\]])", r"\1", sig)  # a reflow adds a trailing comma


def signatures(text, lang):
    """name -> set of normalized signatures declared in this file."""
    lines = text.splitlines()
    out = {}
    for i, line in enumerate(lines):
        name = decl_name(line, lang)
        if not name or name in SKIP_NAMES:
            continue
        if lang == "go":
            # A renamed receiver variable is not a contract change.
            lines[i] = re.sub(r"^func\s*\(\s*[A-Za-z_]\w*\s+", "func(", line)
        sig = signature_at(lines, i)
        out.setdefault(name, set()).add(sig)
    return out


def signature_changed(old, new, lang):
    if lang != "go":
        return old != new
    # Methods of the same name on different types are different functions: only
    # a receiver type present on both sides can have changed its signature.
    def by_receiver(sigs):
        out = {}
        for sig in sigs:
            m = re.match(r"func\(\*?([A-Za-z_]\w*)", sig)
            out.setdefault(m.group(1) if m else "", set()).add(sig)
        return out
    a, b = by_receiver(old), by_receiver(new)
    return any(a[k] != b[k] for k in a.keys() & b.keys())


def blob(repo, rev, path):
    return git(repo, "show", f"{rev}:{path}", check=False)


def mentions(lines, rx):
    return any(rx.search(l) and not COMMENT_RE.match(l) for l in lines)


def candidate(repo, lang, sha, parent, status, diffs):
    """Apply the selection rule to one commit. Returns the sample entry or None."""
    if parent is None or len(status) > MAX_FILES:
        return None
    modified = [p for p, s in status.items() if s == "M" and is_source(p, lang)]
    prod = [p for p in modified if not is_test(p, lang)]
    if len(prod) < 2:
        return None

    # A declaration anywhere in the commit's new tree of changed files: a function
    # that left file A for file B moved, it was not removed.
    before, after, generated = {}, {}, set()
    for p, s in status.items():
        if s == "D" or not is_source(p, lang) or is_test(p, lang):
            continue
        text = blob(repo, sha, p)
        if has_generated_header(text):
            generated.add(p)
            continue
        after[p] = signatures(text, lang)
    for p in prod:
        if p not in generated:
            before[p] = signatures(blob(repo, parent, p), lang)

    definers, kinds, decls = {}, {}, {}
    for p, old in before.items():
        new = after.get(p, {})
        for name, sigs in old.items():
            if name in new:
                if signature_changed(sigs, new[name], lang):
                    definers.setdefault(name, set()).add(p)
                    kinds.setdefault(name, "signature")
                    decls.setdefault(name, (sorted(sigs), sorted(new[name])))
            elif not any(name in other for other in after.values()):
                definers.setdefault(name, set()).add(p)
                kinds.setdefault(name, "removed")
                decls.setdefault(name, (sorted(sigs), []))

    best = None
    usable = [p for p in prod if p not in generated]
    for name, defs in definers.items():
        rx = re.compile(rf"(?<![\w$]){re.escape(name)}(?![\w$])")
        refs = []
        for p in usable:
            if p in defs:
                continue
            removed, added = diffs.get(p, ([], []))
            # A caller of a removed function loses the call.
            if mentions(removed if kinds[name] == "removed" else removed + added, rx):
                refs.append(p)
        if refs and (best is None or (-len(refs), name) < (-len(best[2]), best[0])):
            best = (name, defs, refs)
    if best is None:
        return None
    name, defs, refs = best
    rx = re.compile(rf"(?<![\w$]){re.escape(name)}(?![\w$])")
    tests = [p for p in modified if is_test(p, lang) and p not in defs]
    test_refs = [p for p in tests if mentions(diffs[p][0] + diffs[p][1], rx)]
    return {
        "sha": sha,
        "parent": parent,
        "symbol": name,
        "kind": kinds[name],
        "definers": sorted(defs),
        "decl_before": decls[name][0],
        "decl_after": decls[name][1],
        "truth_all": sorted(p for p in usable if p not in defs),
        "truth_ref": sorted(refs),
        "truth_tests": sorted(tests),
        "truth_tests_ref": sorted(test_refs),
        "files_in_commit": len(status),
    }


def cmd_select(a):
    head = git(a.repo, "rev-parse", a.head).strip()
    pool = 0
    cands = []
    seen = set()
    for sha, parent, status, diffs in parse_commits(a.repo, head):
        pool += 1
        c = candidate(a.repo, a.lang, sha, parent, status, diffs)
        if not c:
            continue
        # Rebased or cherry-picked duplicates are the same change; keep one.
        key = (c["symbol"], tuple(c["decl_before"]), tuple(c["decl_after"]))
        if key in seen:
            continue
        seen.add(key)
        c["rank"] = hashlib.sha256(f"{SEED}:{sha}".encode()).hexdigest()
        cands.append(c)
    cands.sort(key=lambda c: c["rank"])
    out = {
        "lang": a.lang,
        "name": a.name or Path(a.repo).resolve().name,
        "repo": a.repo,  # as given: `run` resolves it against its own working directory
        "head": head,
        "seed": SEED,
        "commits_in_pool": pool,
        "candidates": len(cands),
        "index_lang": a.index_lang or a.lang,
        "prep": a.prep,
        "target": a.count,
        # Order is the sampling order. `run` stops at `target` measured commits,
        # so an entry whose index cannot be built is logged and the next one used.
        "sample": cands[: a.count + a.spare],
    }
    Path(a.out).write_text(json.dumps(out, indent=1) + "\n")
    print(f"{out['name']}: {pool} non-merge commits, {len(cands)} candidates, "
          f"kept {len(out['sample'])} (target {a.count}) -> {a.out}")


# --- SWE-Bench ProMax --------------------------------------------------------

PROMAX_LANGS = {"go": "go", "typescript": "ts", "rust": "rust"}


def promax_commit(repo_dir, inst):
    """Commit an instance's patches on its base commit. Returns (parent, sha).

    Works on the index only, in a blobless clone: deciding whether an instance qualifies
    needs the files it touches, and some of these repos don't fit on a laptop's disk
    once per base commit.
    """
    base = inst["base_commit"]
    git(repo_dir, "fetch", "--quiet", "--depth", "1", "--filter=blob:none", "origin", base)
    git(repo_dir, "read-tree", base)
    for patch in (inst["patch"], inst.get("test_patch") or ""):
        if not patch.strip():
            continue
        r = subprocess.run(["git", "-C", str(repo_dir), "apply", "--whitespace=nowarn", "--cached", "-"],
                           input=patch.encode(), capture_output=True)
        if r.returncode != 0:
            raise RuntimeError(f"patch does not apply: {r.stderr.decode(errors='replace')[:200]}")
    tree = git(repo_dir, "write-tree").strip()
    sha = git(repo_dir, "-c", "user.name=eval", "-c", "user.email=eval@invalid", "commit-tree", tree,
              "-p", base, "-m", inst["instance_id"]).strip()
    git(repo_dir, "update-ref", "refs/heads/promax/" + inst["instance_id"], sha)
    return base, sha


def cmd_promax(a):
    """Turn ProMax instances into samples, one per repo, under the same selection rule.

    The dataset has no license on its card or in its repository, so it stays outside this
    checkout: --data points at a copy, and samples and clones go to --work.
    """
    instances = [i for i in json.loads(Path(a.data).read_text()) if i["language"] in PROMAX_LANGS]
    if a.only:
        instances = [i for i in instances if i["repo"] in a.only.split(",")]
    work = Path(a.work)
    work.mkdir(parents=True, exist_ok=True)
    samples, outcomes = {}, []
    for inst in sorted(instances, key=lambda i: i["instance_id"]):
        lang = PROMAX_LANGS[inst["language"]]
        name = inst["repo"].replace("/", "__")
        repo_dir = work / "repos" / name
        if not repo_dir.exists():
            repo_dir.mkdir(parents=True)
            git(repo_dir, "init", "-q")
            git(repo_dir, "remote", "add", "origin", f"https://github.com/{inst['repo']}.git")
            git(repo_dir, "config", "remote.origin.promisor", "true")
            git(repo_dir, "config", "remote.origin.partialclonefilter", "blob:none")
        outcome = {"instance_id": inst["instance_id"], "repo": inst["repo"], "lang": lang}
        try:
            parent, sha = promax_commit(repo_dir, inst)
            _, _, status, diffs = next(iter(parse_commits(repo_dir, sha, limit=1)))
            outcome["files"] = len(status)
            entry = candidate(repo_dir, lang, sha, parent, status, diffs)
            if entry is None:
                outcome["outcome"] = "over-30-files" if len(status) > MAX_FILES else "no-signature-change-with-caller"
            else:
                entry["instance_id"] = inst["instance_id"]
                outcome["outcome"] = "qualifies"
                sample = samples.setdefault(name, {
                    "lang": lang, "name": name, "repo": str(repo_dir.resolve()), "seed": SEED,
                    "index_lang": lang, "prep": a.prep_ts if lang == "ts" else None,
                    "partial": True, "target": 0, "sample": [],
                })
                sample["sample"].append(entry)
                sample["target"] += 1
        except (RuntimeError, subprocess.SubprocessError, StopIteration) as e:
            outcome["outcome"] = "could-not-prepare"
            outcome["error"] = repr(e)[:300]
        outcomes.append(outcome)
        print(f"{inst['instance_id']} {outcome['outcome']} {outcome.get('files', '')}", flush=True)
    for name, sample in samples.items():
        (work / f"{name}.sample.json").write_text(json.dumps(sample, indent=1) + "\n")
    (work / "outcomes.json").write_text(json.dumps(outcomes, indent=1) + "\n")
    tally = {}
    for o in outcomes:
        key = (o["lang"], o["outcome"])
        tally[key] = tally.get(key, 0) + 1
    for key in sorted(tally):
        print(key, tally[key])


# --- run ---------------------------------------------------------------------

# What the compiler says broke. gopls type-checks every package even when a
# dependency has errors, and tsc checks one program; cargo stops at a broken crate,
# so a Rust result is a lower bound.
CHECKS = {
    "go": "gopls check $(git ls-files '*.go' | grep -v testdata)",
    "ts": "pnpm exec tsc --noEmit --pretty false",
    "rust": "cargo check --workspace --all-targets --keep-going --message-format=short",
}
DIAG_RE = re.compile(r"^(?:\./)?([^\s:()]+\.(?:go|tsx?|mts|cts|rs))[:(](\d+)")


def run_check(cmd, prefix, clone, env, lang, log):
    """path -> number of distinct diagnostics. None if the checker did not run."""
    t = time.time()
    try:
        r = subprocess.run(["sh", "-c", cmd], cwd=clone, env=env, timeout=1200,
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except subprocess.TimeoutExpired:
        return None
    text = r.stdout.decode("utf-8", errors="replace")
    with open(log, "a") as lf:
        lf.write(f"\n$ {cmd}  [rc {r.returncode}, {round(time.time() - t)}s, {len(text.splitlines())} lines]\n")
    seen = set()
    for line in text.splitlines():
        m = DIAG_RE.match(line)
        if not m:
            continue
        if lang != "go" and "error" not in line:
            continue
        msg = re.sub(r"\s*\[[a-z0-9]+,[a-z0-9]+\]$", "", line)  # gopls repeats a line per GOOS,GOARCH
        # gopls prints resolved absolute paths, and on macOS the temp dir is a symlink.
        path = os.path.relpath(os.path.realpath(os.path.join(clone, prefix, m.group(1))),
                               os.path.realpath(clone))
        seen.add((path, msg))
    if not seen and r.returncode not in (0,):
        return None
    counts = {}
    for path, _ in seen:
        counts[path] = counts.get(path, 0) + 1
    return counts


def available_gb():
    if sys.platform != "darwin":
        return 1e9
    out = subprocess.run(["vm_stat"], capture_output=True, text=True).stdout
    page = int(re.search(r"page size of (\d+)", out).group(1))
    pages = 0
    for key in ("free", "inactive", "speculative", "purgeable"):
        m = re.search(rf"Pages {key}:\s+(\d+)", out)
        pages += int(m.group(1)) if m else 0
    return pages * page / 2**30


def wait_for_memory(min_gb, max_wait=1800):
    start = time.time()
    while available_gb() < min_gb:
        if time.time() - start > max_wait:
            return False
        time.sleep(30)
    return True


def sh(cmd, cwd, env, timeout, log):
    """Run cmd. Returns (rc, stdout, seconds); rc -1 is a timeout."""
    t = time.time()
    with open(log, "ab") as lf:
        lf.write(f"\n$ {' '.join(cmd)}\n".encode())
        try:
            r = subprocess.run(cmd, cwd=cwd, env=env, timeout=timeout,
                               stdout=subprocess.PIPE, stderr=lf)
        except subprocess.TimeoutExpired:
            return -1, "", time.time() - t
    return r.returncode, r.stdout.decode("utf-8", errors="replace"), time.time() - t


def parse_json(text):
    start = text.find("{")
    if start < 0:
        raise ValueError("no JSON in output")
    return json.loads(text[start:])


def name_matches(sym_name, target):
    return re.search(rf"(?<![\w$]){re.escape(target)}(?![\w$])", sym_name or "") is not None


def changes_view(data, target, definers, lang):
    affected = data.get("affectedSymbols") or []
    files, direct = set(), set()
    no_location = 0
    for item in affected:
        loc = item.get("location") or {}
        path = loc.get("path") or loc.get("fileId")
        if not path:
            no_location += 1
            continue
        files.add(path)
        if item.get("distance") == 1:
            direct.add(path)
    tests = {t["filePath"] for t in (data.get("affectedTests") or []) if t.get("filePath")}
    contracts = data.get("contracts") or []
    changed = data.get("changedSymbols") or []
    named = [s for s in changed if name_matches(s.get("name"), target) and s.get("file") in definers]
    summary = data.get("summary") or {}
    risk = data.get("riskScore") or {}
    # Absent from a binary older than the field: keeps old results readable.
    cf = data.get("consumerFiles")
    consumer = {}
    if cf is not None:
        consumer = {
            "files_direct": sorted(set(cf.get("direct") or []) - set(definers)),
            "files": sorted((set(cf.get("direct") or []) | set(cf.get("transitive") or [])) - set(definers)),
            "files_tests": sorted(cf.get("tests") or []),
            "contract_changed": sorted(set(cf.get("contractChanged") or []) - set(definers)),
            "contract_changed_tests": sorted(cf.get("contractChangedTests") or []),
            "files_truncated": bool(cf.get("truncated")),
        }
    done = data.get("doneCheck")
    if done is not None:
        sites = done.get("openSites") or []
        consumer.update({
            "done_status": done.get("status"),
            "open_files": sorted({x["file"] for x in sites if not x.get("isTest")} - set(definers)),
            "open_tests": sorted({x["file"] for x in sites if x.get("isTest")}),
            "open_count": done.get("openCount", 0),
            "updated_count": done.get("updatedCount", 0),
        })
    return {
        **consumer,
        "tests_seen_using": sorted({t["filePath"] for t in (data.get("affectedTests") or [])
                                    if t.get("filePath") and t.get("reason") in ("direct", "transitive")}),
        "named_declaration": sorted({s.get("declaration") or "" for s in named}),
        "predicted": sorted(files - set(definers)),
        "predicted_direct": sorted(direct - set(definers)),
        "affected_tests": sorted(tests),
        "affected_items": len(affected),
        "affected_no_location": no_location,
        "affected_total_reported": summary.get("directlyAffected", 0) + summary.get("transitivelyAffected", 0),
        "blast_file_count": (data.get("blastRadius") or {}).get("fileCount"),
        "changed_symbols": len(changed),
        "file_level_entries": sum(1 for s in changed if (s.get("symbolId") or "").startswith("file:")),
        "contract_signal": any(name_matches(c.get("symbol"), target) for c in contracts),
        "named": bool(named),
        "named_ids": sorted({s["symbolId"] for s in named}),
        "named_change_types": sorted({s.get("changeType", "") for s in named}),
        "named_confidence": max((s.get("confidence", 0) for s in named), default=None),
        "risk": summary.get("estimatedRisk"),
        "risk_score": risk.get("score"),
        "index_stale": (data.get("indexStaleness") or {}).get("isStale"),
        "confidence_tier": (data.get("confidence") or {}).get("tier"),
    }


def grep_view(clone, entry, lang):
    out = git(clone, "grep", "-l", "-w", entry["symbol"], entry["parent"], check=False)
    prefix = entry["parent"] + ":"
    raw = [l[len(prefix):] for l in out.splitlines() if l.startswith(prefix)]
    files = {p for p in raw if is_source(p, lang)} - set(entry["definers"])
    return {
        "raw_hits": len(raw),
        "predicted": sorted(p for p in files if not is_test(p, lang)),
        "predicted_tests": sorted(p for p in files if is_test(p, lang)),
    }


def refs_view(ckb, ids, clone, env, log):
    """Files the index itself lists as referencing the named symbols."""
    files = set()
    for sid in ids:
        rc, out, _ = sh([ckb, "refs", sid, "--include-tests", "--limit", "100000", "--format", "json"],
                        clone, env, 300, log)
        if rc != 0:
            continue
        try:
            data = parse_json(out)
        except ValueError:
            continue
        for ref in data.get("references") or []:
            loc = ref.get("location") or {}
            p = loc.get("path") or loc.get("fileId") or ref.get("file")
            if p:
                files.add(p)
    return sorted(files)


def miss_class(path, ckb, uncapped, ref_files, index_bytes):
    if not ckb["named"]:
        return "symbol-not-named"
    if uncapped and path in uncapped["predicted"]:
        return "item-cap"
    if index_bytes is not None and path.encode() not in index_bytes:
        return "file-not-in-index"
    if path in ref_files:
        return "ref-known-not-in-impact"
    return "no-ref-to-target"


def classify_misses(entry, ckb, uncapped, ref_files, index_bytes, at_parent):
    """Why each file of truth_all is missing from the default prediction."""
    out = {}
    predicted = set(ckb["predicted"])
    for path in entry["truth_all"]:
        if path in predicted:
            continue
        if path not in entry["truth_ref"]:
            out[path] = "coedit-no-mention"  # changed lines do not mention the symbol
        elif path not in at_parent and path not in ref_files:
            out[path] = "new-caller"  # the word is not in the file at the parent
        else:
            out[path] = miss_class(path, ckb, uncapped, ref_files, index_bytes)
    return out


def classify_compile_misses(rec, uncapped, ref_files, index_bytes):
    """Why each file the compiler says broke is missing from the prediction."""
    comp = rec.get("compile")
    if not comp:
        return {}
    ckb = rec["ckb"]
    seen = set(ckb["predicted"]) | set(ckb["affected_tests"]) | set(ckb.get("files") or [])
    return {
        p: miss_class(p, ckb, uncapped, ref_files, index_bytes)
        for p in comp["broken"] + comp["broken_tests"] if p not in seen
    }


def configured_index_path(clone):
    try:
        cfg = json.loads((Path(clone) / ".ckb" / "config.json").read_text())
        return cfg["backends"]["scip"]["indexPath"]
    except (OSError, ValueError, KeyError):
        return "index.scip"


def symbol_kind(symbol_id):
    """What a SCIP symbol is, from its descriptor suffix."""
    if symbol_id.endswith("#"):
        return "type"
    if symbol_id.endswith(")."):
        return "function"
    return "other"


def apply_whole_commit(entry, clone):
    """Put the commit's every file in the working tree, on top of its parent."""
    out = git(clone, "diff", "--no-renames", "--name-status", entry["parent"], entry["sha"])
    for line in out.splitlines():
        status, path = line.split("\t", 1)
        target = Path(clone) / path
        if status == "D":
            if target.exists():
                target.unlink()
            continue
        blob = subprocess.run(["git", "-C", str(clone), "show", f"{entry['sha']}:{path}"],
                              capture_output=True, check=True).stdout
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(blob)
        if status == "A":
            git(clone, "add", "-N", path)


def whole_commit_view(entry, clone, ckb, env, log, check_cmd, check_prefix, baseline, lang):
    """The done check once the commit is complete: each open use is a candidate false alarm."""
    time.sleep(1.1)
    apply_whole_commit(entry, clone)
    broken = None
    if check_cmd and baseline is not None:
        after = run_check(check_cmd, check_prefix, str(clone), env, lang, log)
        if after is not None:
            broken = sorted(p for p, n in after.items() if n > baseline.get(p, 0))
    rc, out, _ = sh([ckb, "changes", "--format", "json"], clone, env, 1200, log)
    if rc != 0:
        return {"status": "changes-failed", "broken": broken}
    data = parse_json(out)
    done = data.get("doneCheck") or {}
    kinds = {}
    for sym in data.get("changedSymbols") or []:
        kinds.setdefault(sym.get("name"), symbol_kind(sym.get("symbolId") or ""))
    sites = [{
        "file": x["file"], "line": x.get("line"), "code": x.get("code"), "symbol": x.get("symbol"),
        "change": x.get("change"), "is_test": bool(x.get("isTest")), "unverified": bool(x.get("unverified")),
        "kind": kinds.get(x.get("symbol"), "other"),
        "compiler_broke": None if broken is None else x["file"] in broken,
    } for x in done.get("openSites") or []]
    return {
        "status": done.get("status"),
        "note": done.get("note"),
        "open_count": done.get("openCount", 0),
        "updated_count": done.get("updatedCount", 0),
        "truncated": bool(done.get("truncated")),
        "open_sites": sites,
        "broken": broken,
    }


def measure_one(entry, sample, clone, ckb, env, min_gb, log, check_cmd, check_prefix):
    lang = sample["lang"]
    rec = {"sha": entry["sha"], "lang": lang, "repo": sample["name"], "entry": entry}
    time.sleep(1.1)  # cargo compares mtimes: a write in the same second as its last check goes unseen
    for line in git(clone, "ls-files", "-v").splitlines():
        if line.startswith("S "):
            git(clone, "update-index", "--no-skip-worktree", line[2:])
    git(clone, "checkout", "-qf", entry["parent"])
    git(clone, "clean", "-qfdx", "-e", "node_modules", "-e", "target")
    if not wait_for_memory(min_gb):
        rec["status"] = "skipped-memory"
        return rec

    rc, _, _ = sh([ckb, "init", "--force"], clone, env, 120, log)
    if rc != 0:
        rc, _, _ = sh([ckb, "init"], clone, env, 120, log)
    if sample.get("prep"):
        rc, _, secs = sh(["sh", "-c", sample["prep"]], clone, env, 900, log)
        rec["prep_seconds"] = round(secs)
        rec["prep_rc"] = rc

    index = [ckb, "index", "--force"]
    if sample.get("index_lang"):
        index += ["--lang", sample["index_lang"]]
    rc, _, secs = sh(index, clone, env, 1800, log)
    rec["index_seconds"] = round(secs)
    index_path = Path(clone) / configured_index_path(clone)
    if rc != 0 or not index_path.exists():
        rec["status"] = "index-failed"
        rec["index_rc"] = rc
        return rec
    rec["index_bytes"] = index_path.stat().st_size
    index_bytes = index_path.read_bytes()

    # Old commits track .ckb/ or an index. `ckb init` rewrites them, and that is
    # the harness talking, not the change under test.
    for path in git(clone, "ls-files", ".ckb", ".scip", "index.scip").split():
        git(clone, "update-index", "--skip-worktree", path)

    baseline_diags = run_check(check_cmd, check_prefix, str(clone), env, lang, log) if check_cmd else None

    # Everything from the parent except the file(s) defining the symbol.
    time.sleep(1.1)
    for path in entry["definers"]:
        blob = subprocess.run(["git", "-C", str(clone), "show", f"{entry['sha']}:{path}"],
                              capture_output=True, check=True).stdout
        (Path(clone) / path).write_bytes(blob)
    rec["applied_diff_files"] = git(clone, "diff", "--name-only").split()

    # Compiler truth: files with more diagnostics than at the parent.
    rec["compile"] = None
    if check_cmd and baseline_diags is not None:
        after = run_check(check_cmd, check_prefix, str(clone), env, lang, log)
        if after is not None:
            broken = {p for p, n in after.items() if n > baseline_diags.get(p, 0)}
            broken = {p for p in broken if is_source(p, lang)} - set(entry["definers"])
            rec["compile"] = {
                "broken": sorted(p for p in broken if not is_test(p, lang)),
                "broken_tests": sorted(p for p in broken if is_test(p, lang)),
                "baseline_diagnostics": sum(baseline_diags.values()),
            }

    rc, out, secs = sh([ckb, "changes", "--format", "json"], clone, env, 1200, log)
    rec["changes_seconds"] = round(secs)
    if rc != 0:
        rec["status"] = "changes-failed"
        rec["changes_rc"] = rc
        return rec
    data = parse_json(out)
    rec["ckb"] = changes_view(data, entry["symbol"], entry["definers"], lang)

    # Same index, list cap lifted: how much of a miss is only the cap.
    cfg_path = Path(clone) / ".ckb" / "config.json"
    uncapped = None
    try:
        cfg = json.loads(cfg_path.read_text())
        cfg.setdefault("budget", {})["maxImpactItems"] = 1000000
        cfg_path.write_text(json.dumps(cfg, indent=2))
        rc, out, _ = sh([ckb, "changes", "--format", "json"], clone, env, 1200, log)
        if rc == 0:
            uncapped = changes_view(parse_json(out), entry["symbol"], entry["definers"], lang)
    except (OSError, ValueError):
        pass
    rec["ckb_uncapped"] = uncapped

    rec["ref_files"] = refs_view(ckb, rec["ckb"]["named_ids"], clone, env, log)
    rec["grep"] = grep_view(clone, entry, lang)
    rec["misses"] = classify_misses(entry, rec["ckb"], uncapped, set(rec["ref_files"]), index_bytes,
                                    set(rec["grep"]["predicted"]))
    rec["compile_misses"] = classify_compile_misses(rec, uncapped, set(rec["ref_files"]), index_bytes)
    rec["missed_in_grep"] = sorted(set(entry["truth_all"]) - set(rec["grep"]["predicted"]))
    if "done_status" in rec["ckb"]:
        rec["whole_commit"] = whole_commit_view(entry, clone, ckb, env, log, check_cmd, check_prefix,
                                                baseline_diags, lang)
    rec["status"] = "ok"
    return rec


def cmd_run(a):
    sample = json.loads(Path(a.sample).read_text())
    out_path = Path(a.out)
    done = {}
    if out_path.exists():
        for line in out_path.read_text().splitlines():
            r = json.loads(line)
            done[r["sha"]] = r

    work = Path(tempfile.mkdtemp(prefix="ckb-eval-"))
    clone = work / "clone"
    env = dict(os.environ, CKB_HOME=str(work / "ckb-home"), CKB_NO_UPDATE_CHECK="1",
               GOPLSCACHE=str(work / "gopls-cache"))
    check_cmd = None if a.no_check else (a.check or CHECKS.get(sample["lang"]))
    log = a.log or str(work / "run.log")
    print(f"workdir {work}  log {log}", flush=True)
    try:
        if sample.get("partial"):
            # A blobless clone can't serve its own clones; a copy fetches what it checks out.
            shutil.copytree(sample["repo"], clone, symlinks=True)
        else:
            subprocess.run(["git", "clone", "--quiet", "--no-hardlinks", sample["repo"], str(clone)], check=True)
        target = a.limit or sample["target"]
        measured = sum(1 for r in done.values() if r.get("status") == "ok")
        for entry in sample["sample"]:
            if measured >= target:
                break
            if entry["sha"] in done:
                continue
            t = time.time()
            try:
                rec = measure_one(entry, sample, clone, os.path.abspath(a.ckb), env, a.min_free_gb, log,
                                  check_cmd, a.check_prefix)
            except Exception as e:  # one broken commit must not lose the batch
                rec = {"sha": entry["sha"], "lang": sample["lang"], "repo": sample["name"],
                       "entry": entry, "status": "error", "error": repr(e)[:400]}
            rec["ckb_commit"] = a.ckb_commit
            with out_path.open("a") as f:
                f.write(json.dumps(rec) + "\n")
            measured += rec["status"] == "ok"
            # Symbol names are not printed: this also runs on private repos.
            print(f"{entry['sha'][:10]} {rec['status']} {round(time.time()-t)}s "
                  f"mem-avail {available_gb():.1f}G measured {measured}/{target}", flush=True)
    finally:
        if a.keep:
            print(f"kept {work}")
        else:
            shutil.rmtree(work, ignore_errors=True)
            if work.exists():
                print(f"could not remove {work}")


# --- report ------------------------------------------------------------------


def score(pred, truth):
    pred, truth = set(pred), set(truth)
    hit = len(pred & truth)
    return {
        "hit": hit,
        "pred": len(pred),
        "truth": len(truth),
        "recall": hit / len(truth) if truth else None,
        "precision": hit / len(pred) if pred else None,
    }


def mean(xs):
    xs = [x for x in xs if x is not None]
    return statistics.fmean(xs) if xs else None


def aggregate(scores):
    hit = sum(s["hit"] for s in scores)
    pred = sum(s["pred"] for s in scores)
    truth = sum(s["truth"] for s in scores)
    return {
        "n": len(scores),
        "recall_mean": mean(s["recall"] for s in scores),
        "recall_pooled": hit / truth if truth else None,
        "precision_mean": mean(s["precision"] for s in scores),
        "precision_pooled": hit / pred if pred else None,
        "hit": hit,
        "truth": truth,
        "predicted": pred,
        "empty_predictions": sum(1 for s in scores if s["pred"] == 0),
        "full_recall": sum(1 for s in scores if s["truth"] and s["hit"] == s["truth"]),
        "zero_recall": sum(1 for s in scores if s["truth"] and s["hit"] == 0),
    }


def analyse(records):
    ok = [r for r in records if r["status"] == "ok"]
    tools = {
        "ckb": lambda r: r["ckb"]["predicted"],
        "ckb_direct_only": lambda r: r["ckb"]["predicted_direct"],
        "ckb_uncapped": lambda r: (r.get("ckb_uncapped") or r["ckb"])["predicted"],
        "grep": lambda r: r["grep"]["predicted"],
    }
    # consumerFiles, from binaries that have it. Same scoring, other lists.
    if ok and "files" in ok[0]["ckb"]:
        tools.update({
            "ckb_files": lambda r: r["ckb"]["files"],
            "ckb_files_direct": lambda r: r["ckb"]["files_direct"],
            "ckb_contract": lambda r: r["ckb"]["contract_changed"],
        })
    if ok and "open_files" in ok[0]["ckb"]:
        tools["ckb_open"] = lambda r: r["ckb"]["open_files"]
    result_tools = list(tools)
    def non_test(r, files):
        return [p for p in files if not is_test(p, r["lang"])]

    result = {
        "attempted": len(records),
        "measured": len(ok),
        "status": {},
        "truth_all": {},
        "truth_ref": {},
        "tests": {},
        "tools": result_tools,
    }
    for r in records:
        result["status"][r["status"]] = result["status"].get(r["status"], 0) + 1
    for name, fn in tools.items():
        result["truth_all"][name] = aggregate(
            [score(non_test(r, fn(r)), r["entry"]["truth_all"]) for r in ok])
        result["truth_ref"][name] = aggregate(
            [score(non_test(r, fn(r)), r["entry"]["truth_ref"]) for r in ok])

    def ckb_tests(r):
        return set(r["ckb"]["affected_tests"]) | {p for p in r["ckb"]["predicted"] if is_test(p, r["lang"])}
    with_tests = [r for r in ok if r["entry"]["truth_tests_ref"]]
    missed_tests = [(r, p) for r in with_tests for p in r["entry"]["truth_tests_ref"] if p not in ckb_tests(r)]
    result["tests"] = {
        "n_with_test_edits": len(with_tests),
        "missed": len(missed_tests),
        "missed_but_in_refs": sum(1 for r, p in missed_tests if p in r["ref_files"]),
        "ckb": aggregate([score(ckb_tests(r), r["entry"]["truth_tests_ref"]) for r in with_tests]),
        "grep": aggregate([score(r["grep"]["predicted_tests"], r["entry"]["truth_tests_ref"]) for r in with_tests]),
    }
    if ok and "files" in ok[0]["ckb"]:
        result["tests"]["ckb_seen_using"] = aggregate(
            [score(r["ckb"]["tests_seen_using"], r["entry"]["truth_tests_ref"]) for r in with_tests])

    compiled = [r for r in ok if r.get("compile")]
    broke = [r for r in compiled if r["compile"]["broken"]]
    broke_t = [r for r in compiled if r["compile"]["broken_tests"]]
    result["compile"] = {
        "checked": len(compiled),
        "no_breakage": sum(1 for r in compiled if not r["compile"]["broken"] and not r["compile"]["broken_tests"]),
        "non_test": {name: aggregate([score(non_test(r, fn(r)), r["compile"]["broken"]) for r in broke])
                     for name, fn in tools.items()},
        "tests": {
            "ckb": aggregate([score(ckb_tests(r), r["compile"]["broken_tests"]) for r in broke_t]),
            "grep": aggregate([score(r["grep"]["predicted_tests"], r["compile"]["broken_tests"]) for r in broke_t]),
        },
    }
    if ok and "files" in ok[0]["ckb"]:
        result["compile"]["tests"]["ckb_contract"] = aggregate(
            [score(r["ckb"]["contract_changed_tests"], r["compile"]["broken_tests"]) for r in broke_t])
    if ok and "open_files" in ok[0]["ckb"]:
        result["compile"]["tests"]["ckb_open"] = aggregate(
            [score(r["ckb"]["open_tests"], r["compile"]["broken_tests"]) for r in broke_t])
        result["done_status_defining_file"] = {}
        for r in ok:
            status = r["ckb"]["done_status"]
            result["done_status_defining_file"][status] = result["done_status_defining_file"].get(status, 0) + 1
    whole = [r["whole_commit"] for r in ok if r.get("whole_commit")]
    if whole:
        sites = [x for w in whole for x in w.get("open_sites") or []]
        tally = {}
        for x in sites:
            tally[x["kind"]] = tally.get(x["kind"], 0) + 1
        statuses = {}
        for w in whole:
            statuses[w["status"]] = statuses.get(w["status"], 0) + 1
        per_commit = [w.get("open_count", 0) for w in whole]
        result["whole_commit"] = {
            "commits": len(whole),
            "status": statuses,
            "commits_with_open": sum(1 for n in per_commit if n),
            "open_sites": sum(per_commit),
            "open_median": statistics.median(per_commit),
            "open_max": max(per_commit),
            "by_kind": tally,
            "tests": sum(1 for x in sites if x["is_test"]),
            "unverified": sum(1 for x in sites if x["unverified"]),
            "compiler_broke": sum(1 for x in sites if x["compiler_broke"] is True),
            "false_alarm": sum(1 for x in sites if x["compiler_broke"] is False),
            "no_compiler": sum(1 for x in sites if x["compiler_broke"] is None),
            "commits_broken": sum(1 for w in whole if w.get("broken")),
        }
    # What the direct list got wrong: uses of the target that the compiler let through,
    # or uses of another symbol in the applied file.
    wrong = {"target_not_broken": 0, "other_symbol": 0, "right": 0}
    for r in compiled:
        comp = r["compile"]
        refs = set(r.get("ref_files") or [])
        for p in non_test(r, r["ckb"]["predicted_direct"]):
            if p in comp["broken"]:
                wrong["right"] += 1
            elif p in refs:
                wrong["target_not_broken"] += 1
            else:
                wrong["other_symbol"] += 1
    result["compile"]["direct_wrong"] = wrong
    causes_c = {}
    for r in ok:
        for cause in (r.get("compile_misses") or {}).values():
            causes_c.setdefault(cause, {"files": 0, "commits": set()})
            causes_c[cause]["files"] += 1
            causes_c[cause]["commits"].add(r["sha"])
    result["compile_miss_causes"] = {
        k: {"files": v["files"], "commits": len(v["commits"])}
        for k, v in sorted(causes_c.items(), key=lambda kv: -len(kv[1]["commits"]))
    }
    cap = {"truth_all_commits": 0, "truth_all_files": 0, "compile_commits": 0, "compile_files": 0}
    for r in ok:
        if not r.get("ckb_uncapped"):
            continue
        extra = set(r["ckb_uncapped"]["predicted"]) - set(r["ckb"]["predicted"])
        a_lost = extra & set(r["entry"]["truth_all"])
        comp = r.get("compile") or {"broken": [], "broken_tests": []}
        c_lost = extra & set(comp["broken"] + comp["broken_tests"])
        cap["truth_all_commits"] += bool(a_lost)
        cap["truth_all_files"] += len(a_lost)
        cap["compile_commits"] += bool(c_lost)
        cap["compile_files"] += len(c_lost)
    result["cap_lost"] = cap
    result["capped_commits"] = sum(1 for r in ok if r["ckb"]["affected_items"] < r["ckb"]["affected_total_reported"])
    result["named"] = sum(1 for r in ok if r["ckb"]["named"])
    result["contract_signal"] = sum(1 for r in ok if r["ckb"].get("contract_signal"))
    result["named_no_consumers"] = sum(1 for r in ok if r["ckb"]["named"] and not r["ckb"]["predicted"])
    result["worse_than_grep"] = sum(
        1 for r in ok
        if score(non_test(r, r["ckb"]["predicted"]), r["entry"]["truth_all"])["hit"]
        < score(r["grep"]["predicted"], r["entry"]["truth_all"])["hit"])
    result["better_than_grep"] = sum(
        1 for r in ok
        if score(non_test(r, r["ckb"]["predicted"]), r["entry"]["truth_all"])["hit"]
        > score(r["grep"]["predicted"], r["entry"]["truth_all"])["hit"])

    risk = {}
    for r in ok:
        row = risk.setdefault(r["ckb"]["risk"] or "none",
                              {"commits": 0, "truth_files": [], "truth_ref_files": [], "broken": []})
        comp = r.get("compile")
        if comp:
            row["broken"].append(len(comp["broken"]) + len(comp["broken_tests"]))
        row["commits"] += 1
        row["truth_files"].append(len(r["entry"]["truth_all"]))
        row["truth_ref_files"].append(len(r["entry"]["truth_ref"]))
    result["risk"] = {
        k: {"commits": v["commits"],
            "median_truth_files": statistics.median(v["truth_files"]),
            "median_truth_ref_files": statistics.median(v["truth_ref_files"]),
            "median_compile_broken": statistics.median(v["broken"]) if v["broken"] else None,
            "max_compile_broken": max(v["broken"]) if v["broken"] else None,
            "zero_compile_broken": sum(1 for x in v["broken"] if x == 0)}
        for k, v in risk.items()
    }

    causes = {}
    for r in ok:
        for cause in r["misses"].values():
            causes.setdefault(cause, {"files": 0, "commits": set()})
            causes[cause]["files"] += 1
            causes[cause]["commits"].add(r["sha"])
    result["miss_causes"] = {
        k: {"files": v["files"], "commits": len(v["commits"])}
        for k, v in sorted(causes.items(), key=lambda kv: -len(kv[1]["commits"]))
    }
    result["seconds"] = {
        "index_median": statistics.median([r["index_seconds"] for r in ok]) if ok else None,
        "changes_median": statistics.median([r["changes_seconds"] for r in ok]) if ok else None,
    }
    return result


def pct(x):
    return "n/a" if x is None else f"{100 * x:.0f}%"


def report_markdown(records, redact):
    res = analyse(records)
    w = []
    w.append(f"Attempted {res['attempted']}, measured {res['measured']}; status counts: {res['status']}.")
    w.append(f"Target symbol named in changedSymbols: {res['named']}/{res['measured']} "
             f"(named but zero consumers predicted: {res['named_no_consumers']}; "
             f"flagged as possible contract change: {res['contract_signal']}). "
             f"CKB found fewer truth files than grep in {res['worse_than_grep']} commits, "
             f"more in {res['better_than_grep']}. "
             f"affectedSymbols cut by the item cap in {res['capped_commits']} commits, which hid "
             f"{res['cap_lost']['truth_all_files']} truth files in {res['cap_lost']['truth_all_commits']} commits and "
             f"{res['cap_lost']['compile_files']} compiler-broken files in {res['cap_lost']['compile_commits']}.")
    w.append("")
    for key, title in (("truth_all", "Truth = all other non-test source files modified in the commit"),
                       ("truth_ref", "Truth = those whose changed lines mention the symbol")):
        w.append(f"### {title}")
        w.append("")
        w.append("| Tool | n | recall (mean) | recall (pooled) | precision (mean) | precision (pooled) | hit/truth | predicted files | empty | full recall | zero recall |")
        w.append("|---|---|---|---|---|---|---|---|---|---|---|")
        for tool in res["tools"]:
            a = res[key][tool]
            w.append(f"| {tool} | {a['n']} | {pct(a['recall_mean'])} | {pct(a['recall_pooled'])} | "
                     f"{pct(a['precision_mean'])} | {pct(a['precision_pooled'])} | {a['hit']}/{a['truth']} | "
                     f"{a['predicted']} | {a['empty_predictions']} | {a['full_recall']} | {a['zero_recall']} |")
        w.append("")
    c = res["compile"]
    w.append(f"### Truth = files the compiler says broke ({c['checked']} commits checked, {c['no_breakage']} with no breakage)")
    w.append("")
    w.append("| Tool | n | recall (mean) | recall (pooled) | precision (mean) | precision (pooled) | hit/truth | predicted files | zero recall |")
    w.append("|---|---|---|---|---|---|---|---|---|")
    for tool in res["tools"]:
        a = c["non_test"][tool]
        w.append(f"| {tool} | {a['n']} | {pct(a['recall_mean'])} | {pct(a['recall_pooled'])} | "
                 f"{pct(a['precision_mean'])} | {pct(a['precision_pooled'])} | {a['hit']}/{a['truth']} | "
                 f"{a['predicted']} | {a['zero_recall']} |")
    for tool in c["tests"]:
        a = c["tests"][tool]
        w.append(f"| test files: {tool} | {a['n']} | {pct(a['recall_mean'])} | {pct(a['recall_pooled'])} | "
                 f"{pct(a['precision_mean'])} | {pct(a['precision_pooled'])} | {a['hit']}/{a['truth']} | "
                 f"{a['predicted']} | {a['zero_recall']} |")
    w.append("")
    d = c["direct_wrong"]
    w.append(f"Direct files against the compiler (of commits checked): {d['right']} broke, "
             f"{d['target_not_broken']} use the target but didn't break, {d['other_symbol']} only use another symbol in the applied file.")
    w.append("")
    w.append("Compiler-broken files missed by CKB, by class (test files included):")
    w.append("")
    w.append("| Class | commits | files |")
    w.append("|---|---|---|")
    for k, v in res["compile_miss_causes"].items():
        w.append(f"| {k} | {v['commits']} | {v['files']} |")
    w.append("")
    t = res["tests"]
    w.append(f"### Test files edited in the commit and mentioning the symbol ({t['n_with_test_edits']} commits)")
    w.append("")
    w.append("| Tool | recall (mean) | hit/truth | predicted test files |")
    w.append("|---|---|---|---|")
    for tool in t:
        if tool not in ("ckb", "ckb_seen_using", "grep"):
            continue
        a = t[tool]
        w.append(f"| {tool} | {pct(a['recall_mean'])} | {a['hit']}/{a['truth']} | {a['predicted']} |")
    w.append("")
    w.append(f"Of {t['missed']} missed, {t['missed_but_in_refs']} are references the index has (`ckb refs --include-tests`).")
    w.append("")
    if "whole_commit" in res:
        h = res["whole_commit"]
        w.append(f"### Done check with the whole commit applied ({h['commits']} commits)")
        w.append("")
        w.append(f"Status: {h['status']}. With only the defining file applied: {res.get('done_status_defining_file')}.")
        w.append("")
        w.append(f"{h['commits_with_open']} of {h['commits']} complete commits still report an open use: "
                 f"{h['open_sites']} open uses, median {h['open_median']} per commit, max {h['open_max']}; "
                 f"{h['tests']} listed in test files, {h['unverified']} unverified. "
                 f"By kind of changed symbol: {h['by_kind']}.")
        w.append("")
        w.append(f"Listed uses against the compiler: {h['false_alarm']} in a file that didn't break (false alarm), "
                 f"{h['compiler_broke']} in a file with more diagnostics than at the parent, "
                 f"{h['no_compiler']} without a compiler answer. {h['commits_broken']} commits add diagnostics somewhere.")
        w.append("")
    w.append("### Risk level against what the commit had to touch")
    w.append("")
    w.append("| Risk | commits | median truth files | median mentioning files | median compile-broken files | max | commits with none broken |")
    w.append("|---|---|---|---|---|---|---|")
    for k in ("low", "medium", "high", "critical", "none"):
        if k in res["risk"]:
            v = res["risk"][k]
            w.append(f"| {k} | {v['commits']} | {v['median_truth_files']} | {v['median_truth_ref_files']} | {v['median_compile_broken']} | {v['max_compile_broken']} | {v['zero_compile_broken']} |")
    for k, v in res["risk"].items():
        if k not in ("low", "medium", "high", "critical", "none"):
            w.append(f"| {k} | {v['commits']} | {v['median_truth_files']} | {v['median_truth_ref_files']} | {v['median_compile_broken']} | {v['max_compile_broken']} | {v['zero_compile_broken']} |")
    w.append("")
    w.append("### Why truth files were missed (default output)")
    w.append("")
    w.append("| Class | commits | files |")
    w.append("|---|---|---|")
    for k, v in res["miss_causes"].items():
        w.append(f"| {k} | {v['commits']} | {v['files']} |")
    w.append("")
    if not redact:
        w.append("### Per commit")
        w.append("")
        w.append("| Commit | Symbol | Status | Named | Declaration | Risk | truth | CKB hit/pred | uncapped hit/pred | grep hit/pred | mention truth | CKB hit | grep hit | broken | CKB hit | direct hit/pred | contract hit/pred | grep hit/pred | broken tests | CKB hit | grep hit | Miss classes |")
        w.append("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
        for r in records:
            e = r["entry"]
            if r["status"] != "ok":
                w.append(f"| {r['sha'][:9]} | `{e['symbol']}` | {r['status']} | | | | {len(e['truth_all'])} | | | | | | | | | | | | | | | |")
                continue
            c, u, g = r["ckb"], r.get("ckb_uncapped") or r["ckb"], r["grep"]
            nt = lambda files: [p for p in files if not is_test(p, r["lang"])]
            sc, su, sg = (score(nt(x), e["truth_all"]) for x in (c["predicted"], u["predicted"], g["predicted"]))
            rc, rg = score(nt(c["predicted"]), e["truth_ref"]), score(g["predicted"], e["truth_ref"])
            classes = {}
            for v in r["misses"].values():
                classes[v] = classes.get(v, 0) + 1
            cl = ", ".join(f"{k} {v}" for k, v in sorted(classes.items())) or "-"
            comp = r.get("compile") or {"broken": [], "broken_tests": []}
            bc = score(nt(c["predicted"]), comp["broken"])["hit"]
            test_seen = set(c["affected_tests"]) | set(c["predicted"])
            tc, tg = score(test_seen, comp["broken_tests"])["hit"], score(g["predicted_tests"], comp["broken_tests"])["hit"]
            bd = score(nt(c["predicted_direct"]), comp["broken"])
            bk = score(nt(c.get("contract_changed") or []), comp["broken"])
            bgs = score(g["predicted"], comp["broken"])
            decl = ",".join(c.get("named_declaration") or []) or "-"
            contract = f"{bk['hit']}/{bk['pred']}" if "contract_changed" in c else "-"
            w.append(f"| {r['sha'][:9]} | `{e['symbol']}` | ok | {'yes' if c['named'] else 'no'} | {decl} | {c['risk']} | "
                     f"{len(e['truth_all'])} | {sc['hit']}/{sc['pred']} | {su['hit']}/{su['pred']} | "
                     f"{sg['hit']}/{sg['pred']} | {len(e['truth_ref'])} | {rc['hit']} | {rg['hit']} | "
                     f"{len(comp['broken'])} | {bc} | {bd['hit']}/{bd['pred']} | {contract} | {bgs['hit']}/{bgs['pred']} | "
                     f"{len(comp['broken_tests'])} | {tc} | {tg} | {cl} |")
        w.append("")
        if "whole_commit" in res:
            w.append("### Open uses with the whole commit applied, per commit")
            w.append("")
            w.append("| Commit | Status | Touched | Open | Open use | Kind | Compiler broke the file |")
            w.append("|---|---|---|---|---|---|---|")
            for r in records:
                h = r.get("whole_commit")
                if not h:
                    continue
                sites = h.get("open_sites") or [None]
                for x in sites[:10]:
                    use = f"`{x['file']}:{x['line']}` `{x['symbol']}`" if x else "-"
                    w.append(f"| {r['sha'][:9]} | {h['status']} | {h.get('updated_count')} | {h.get('open_count')} | {use} | "
                             f"{x['kind'] if x else '-'} | {x['compiler_broke'] if x else '-'} |")
            w.append("")
    return "\n".join(w)


def cmd_report(a):
    records = [json.loads(l) for l in Path(a.results).read_text().splitlines() if l.strip()]
    if a.json:
        print(json.dumps(analyse(records), indent=1))
    else:
        print(report_markdown(records, a.redact))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("select", help="apply the selection rule and write the sample")
    s.add_argument("--repo", required=True)
    s.add_argument("--lang", required=True, choices=sorted(DECLS))
    s.add_argument("--head", default="HEAD", help="commit the history pool ends at; pin it for a stable sample")
    s.add_argument("--count", type=int, default=15, help="commits to measure")
    s.add_argument("--spare", type=int, default=10, help="extra entries used only when one cannot be measured")
    s.add_argument("--name", help="label for the repo in results")
    s.add_argument("--index-lang", default=None, help="value for `ckb index --lang`; default --lang")
    s.add_argument("--prep", default=None, help="shell command run in the clone before indexing, e.g. a dependency install")
    s.add_argument("--out", required=True)
    s.set_defaults(fn=cmd_select)

    r = sub.add_parser("run", help="measure the sample; resumes if --out exists")
    r.add_argument("--sample", required=True)
    r.add_argument("--ckb", required=True, help="ckb binary")
    r.add_argument("--ckb-commit", default=None, help="recorded in results: the commit the binary was built from")
    r.add_argument("--out", required=True)
    r.add_argument("--limit", type=int, default=None, help="measure this many instead of the sample's target")
    r.add_argument("--min-free-gb", type=float, default=3.0, help="wait for this much reclaimable memory before each index")
    r.add_argument("--log", default=None, help="indexer and ckb stderr goes here (default: workdir, removed at the end)")
    r.add_argument("--check", default=None, help="compiler command, run in the clone; default per language")
    r.add_argument("--check-prefix", default="", help="directory the compiler's paths are relative to")
    r.add_argument("--no-check", action="store_true", help="skip the compiler truth")
    r.add_argument("--keep", action="store_true", help="keep the clone and CKB_HOME")
    r.set_defaults(fn=cmd_run)

    m = sub.add_parser("promax", help="turn SWE-Bench ProMax instances into samples")
    m.add_argument("--data", required=True, help="a local copy of swe-bench-promax.json")
    m.add_argument("--work", required=True, help="clones, samples and outcomes go here")
    m.add_argument("--only", default=None, help="comma-separated repos, e.g. cli/cli")
    m.add_argument("--prep-ts", default=None, help="prep command for TS samples")
    m.set_defaults(fn=cmd_promax)

    o = sub.add_parser("report", help="aggregate a results file")
    o.add_argument("--results", required=True)
    o.add_argument("--redact", action="store_true", help="aggregates only: no commit, symbol or file names")
    o.add_argument("--json", action="store_true")
    o.set_defaults(fn=cmd_report)

    a = p.parse_args()
    a.fn(a)


if __name__ == "__main__":
    main()
