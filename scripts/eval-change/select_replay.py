#!/usr/bin/env python3
"""Pick the commits replay.py measures in a repo, by rule and before anything is measured.

From HEAD back, without merges, at most --max-files files: a commit counts when a non-test
source file of the language changes an exported declaration whose name appears as a word in
another tracked source file at the parent. The files with such a declaration are the ones
replay.py applies. Nothing here reads CKB, so the choice can't favour it. Whether the parent
builds is the driver's job: it needs the compiler, and the order here is the order to try.

    select_replay.py --repo DIR --lang ts|rust --out sample.json [--count 20] [--scan 400]

"Changes a declaration": for a function, a line from its name to the opening brace; for a
type, a struct, an enum, an interface, a trait or a const, any line of it, since its body is
its shape. The extent of a declaration runs to the next line at its indent or less that
doesn't close it. Text, not a parser. Python 3 stdlib.
"""

import argparse
import json
import re
import subprocess
from pathlib import Path

EXTS = {"ts": (".ts", ".tsx", ".mts", ".cts"), "rust": (".rs",)}
GENERATED = re.compile(
    r"(^|/)(vendor|node_modules|third_party|testdata|dist|build|target|generated|\.next|\.claude)/"
    r"|\.d\.ts$|\.gen\.tsx?$|\.generated\.tsx?$")
TESTS = {
    "ts": re.compile(r"(\.|_)(test|spec)\.[cm]?tsx?$|(^|/)(__tests__|tests?|e2e)/"),
    "rust": re.compile(r"_tests?\.rs$|(^|/)(tests|benches)/"),
}
DECL = {
    "ts": re.compile(r"^export\s+(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:async\s+)?"
                     r"(?P<kind>function\*?|const|let|var|class|interface|type|enum)\s+(?P<name>[A-Za-z_$][\w$]*)"),
    "rust": re.compile(r"^(?P<indent>\s*)pub(?:\([^)]*\))?\s+(?:async\s+)?(?:unsafe\s+)?(?:const\s+(?=fn))?"
                       r"(?P<kind>fn|struct|enum|trait|type|const|static|union)\s+(?P<name>[A-Za-z_]\w*)"),
}
FUNCTION_KINDS = {"function", "function*", "fn"}


def git(repo, *args, check=True):
    return subprocess.run(["git", "-C", str(repo), *args], capture_output=True, text=True, check=check).stdout


def is_source(path, lang):
    return path.endswith(EXTS[lang]) and not GENERATED.search(path) and not TESTS[lang].search(path)


def indent(line):
    return len(line) - len(line.lstrip())


def declarations(lines, lang):
    """(name, kind, first, head_end, end) per exported declaration, 1-based, inclusive."""
    out = []
    for i, line in enumerate(lines):
        m = DECL[lang].match(line)
        if not m:
            continue
        own = indent(line)
        end = i
        for j in range(i + 1, min(len(lines), i + 2000)):
            text = lines[j]
            if not text.strip():
                continue
            if indent(text) <= own and text.lstrip()[0] not in "})]":
                break
            end = j
        head = i
        if m["kind"] in FUNCTION_KINDS:
            for j in range(i, end + 1):
                head = j
                if "{" in lines[j] or lines[j].rstrip().endswith(";"):
                    break
        else:
            head = end
        out.append((m["name"], m["kind"], i + 1, head + 1, end + 1))
    return out


HUNK = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+\d+(?:,\d+)? @@", re.M)


def changed_old_lines(repo, parent, sha, path):
    """Old-side lines the commit removed or changed, and the old line after which it
    inserted, so a line added inside a declaration counts for it."""
    lines = set()
    for h in HUNK.finditer(git(repo, "diff", "-U0", "--no-color", parent, sha, "--", path)):
        start, count = int(h[1]), int(h[2] if h[2] is not None else 1)
        if count == 0:
            lines.add(start)  # Insertion after old line `start`
            lines.add(start + 1)
        else:
            lines.update(range(start, start + count))
    return lines


def used_elsewhere(repo, parent, name, path, lang):
    globs = [f"*{e}" for e in EXTS[lang]]
    out = git(repo, "grep", "-l", "-w", "-F", name, parent, "--", *globs, check=False)
    for line in out.splitlines():
        other = line.split(":", 1)[1] if ":" in line else line
        if other != path and not GENERATED.search(other):
            return True
    return False


def candidate(repo, lang, sha, parent, max_files):
    status = git(repo, "diff", "--name-status", "--no-renames", parent, sha).splitlines()
    if not status or len(status) > max_files:
        return None
    files, decls = [], []
    for row in status:
        kind, path = row.split("\t", 1)
        if kind != "M" or not is_source(path, lang):
            continue
        old = git(repo, "show", f"{parent}:{path}", check=False).splitlines()
        touched = changed_old_lines(repo, parent, sha, path)
        hit = []
        for name, k, first, head, end in declarations(old, lang):
            if any(first <= n <= head for n in touched) and used_elsewhere(repo, parent, name, path, lang):
                hit.append(f"{k} {name}")
        if hit:
            files.append(path)
            decls.extend(f"{path}: {h}" for h in hit)
    if not files:
        return None
    return {"sha": sha, "parent": parent, "files": files, "decls": decls, "files_in_commit": len(status)}


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--repo", required=True, type=Path)
    ap.add_argument("--lang", required=True, choices=sorted(EXTS))
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--head", default="HEAD")
    ap.add_argument("--count", type=int, default=20, help="candidates to keep, in order; the driver takes the first that build")
    ap.add_argument("--scan", type=int, default=400, help="commits to look at from HEAD back")
    ap.add_argument("--max-files", type=int, default=30)
    a = ap.parse_args()

    head = git(a.repo, "rev-parse", a.head).strip()
    log = git(a.repo, "log", "--no-merges", f"-n{a.scan}", "--format=%H %P", head).splitlines()
    picked = []
    for row in log:
        parts = row.split()
        if len(parts) != 2:
            continue
        c = candidate(a.repo, a.lang, parts[0], parts[1], a.max_files)
        if c:
            picked.append(c)
            if len(picked) >= a.count:
                break
    a.out.write_text(json.dumps({"repo": str(a.repo.resolve()), "lang": a.lang, "head": head,
                                 "scanned": len(log), "rule": __doc__.split("\n\n")[1], "sample": picked}, indent=1) + "\n")
    print(f"{a.repo.name}: {len(log)} commits scanned, {len(picked)} candidates -> {a.out}")


if __name__ == "__main__":
    main()
