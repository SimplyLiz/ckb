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

`--rule users` is the rule eval.py uses for Go: a changed declaration counts only when
another non-test source file of the same commit, not itself applied, has a removed or added
line naming it as a word, so the commit edited a user along with it. Only the files with
such a declaration are applied, and the users stay at the parent: the change is made where
the agent starts and the users are what it still has to do. The default rule takes every
changed declaration that is named elsewhere, and most of those break nothing.

`--rule variants` is `--rule users` narrowed to Rust enums that gained a variant: a
`pub enum` of a modified file has a variant at the commit that it lacks at the parent,
and another non-test source file of the commit, not applied, has a removed or added line
naming the enum. Only the files with such an enum are applied. It looks for the case the
done-check's enum variant check is for, so that check is measured on commits it wasn't
built from.

`--rule arms` looks for the arms the variant check finds through the variants' own
references: `Self::A` in an impl of the enum, a bare `A` in a file that imports `E::*`,
and `E::A` nested in another pattern, `Some(E::A)`. A modified non-test file has an
enum, public or not, with a variant at the commit that it lacks at the parent, and at
the parent a line of any Rust file holds such an arm over one of the old variants (a
regex over the line: `Self::A … =>`, `A … =>` in a glob-importing file, `(E::A … =>`).
No user has to be edited: an arm in the applied file shows whether the check sees it
updated, one elsewhere whether it sees it open. Only the files with such an enum are
applied. The arms are listed with each candidate, so every one can be looked up.
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


def diff_lines(repo, parent, sha, path):
    """Removed and added lines of one file, without the +++/--- headers."""
    out = []
    for line in git(repo, "diff", "-U0", "--no-color", parent, sha, "--", path).splitlines():
        if line[:1] in "+-" and not line.startswith(("+++", "---")):
            out.append(line[1:])
    return out


def edited_users(repo, lang, sha, parent, status, names, applied):
    """name -> files of the commit, not applied, whose diff names it."""
    others = [row.split("\t", 1)[1] for row in status
              if row.split("\t", 1)[0] == "M" and is_source(row.split("\t", 1)[1], lang)]
    out = {}
    for path in others:
        if path in applied:
            continue
        text = "\n".join(diff_lines(repo, parent, sha, path))
        for name in names:
            if re.search(rf"(?<![\w$]){re.escape(name)}(?![\w$])", text):
                out.setdefault(name, []).append(path)
    return out


RUST_ENUM = re.compile(r"^\s*pub(?:\([^)]*\))?\s+enum\s+([A-Za-z_]\w*)[^{;]*\{", re.M)
RUST_ANY_ENUM = re.compile(r"^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+([A-Za-z_]\w*)[^{;]*\{", re.M)


def enum_variants(text, pattern=RUST_ENUM):
    """name -> variant names of each `pub enum` in a Rust file. Text, not a parser."""
    out = {}
    for m in pattern.finditer(text):
        i, depth = m.end(), 1
        j = i
        while j < len(text) and depth:
            depth += {"{": 1, "}": -1}.get(text[j], 0)
            j += 1
        body = re.sub(r"//[^\n]*|/\*.*?\*/|#\[[^\]]*\]", "", text[i:j - 1], flags=re.S)
        parts, level, cur = [], 0, ""
        for ch in body:
            level += 1 if ch in "({[<" else -1 if ch in ")}]>" else 0
            if ch == "," and level == 0:
                parts.append(cur)
                cur = ""
            else:
                cur += ch
        parts.append(cur)
        out[m[1]] = [v[1] for v in (re.match(r"\s*([A-Za-z_]\w*)", p) for p in parts) if v]
    return out


def gained_variant(repo, parent, sha, path, name):
    old = enum_variants(git(repo, "show", f"{parent}:{path}", check=False)).get(name)
    new = enum_variants(git(repo, "show", f"{sha}:{path}", check=False)).get(name)
    return old is not None and new is not None and any(v not in old for v in new)


def grep_at(repo, rev, pattern):
    """path:line:text hits of a Perl regex in the Rust files of a commit."""
    out = git(repo, "grep", "-n", "-P", "-e", pattern, rev, "--", "*.rs", check=False)
    return [line.split(":", 1)[1] for line in out.splitlines() if line]


def enum_arms(repo, parent, enum, old):
    """The arms over the old variants spelled Self::A, A through a glob, or nested."""
    alt = "|".join(map(re.escape, old))
    globbing = {h.split(":", 1)[0] for h in grep_at(repo, parent, rf"\buse\b.*\b{enum}::\*")}
    arms = {
        "self": grep_at(repo, parent, rf"\bSelf::({alt})\b[^=]*=>|\|\s*Self::({alt})\b"),
        "glob": [h for h in grep_at(repo, parent, rf"^\s*\|?\s*({alt})\b[^=:]*=>") if h.split(":", 1)[0] in globbing],
        "nested": grep_at(repo, parent, rf"[(,\[{{]\s*&?\s*(\w+::)*{enum}::({alt})\b[^;]*=>"),
    }
    return {kind: [":".join(h.split(":", 2)[:2]) for h in hits] for kind, hits in arms.items() if hits}


def arms_candidate(repo, sha, parent, max_files):
    status = git(repo, "diff", "--name-status", "--no-renames", parent, sha).splitlines()
    if not status or len(status) > max_files:
        return None
    files, decls, arms = [], [], {}
    for row in status:
        kind, path = row.split("\t", 1)
        if kind != "M" or not is_source(path, "rust"):
            continue
        old = enum_variants(git(repo, "show", f"{parent}:{path}", check=False), RUST_ANY_ENUM)
        new = enum_variants(git(repo, "show", f"{sha}:{path}", check=False), RUST_ANY_ENUM)
        for name, variants in new.items():
            if not old.get(name) or all(v in old[name] for v in variants):
                continue
            found = enum_arms(repo, parent, name, old[name])
            if found:
                decls.append(f"{path}: enum {name}")
                arms[name] = found
                if path not in files:
                    files.append(path)
    if not files:
        return None
    return {"sha": sha, "parent": parent, "files": files, "decls": decls, "arms": arms,
            "files_in_commit": len(status)}


def candidate(repo, lang, sha, parent, max_files, rule="declaration"):
    if rule == "arms":
        return arms_candidate(repo, sha, parent, max_files) if lang == "rust" else None
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
                hit.append((k, name))
        if hit:
            files.append(path)
            decls.extend((path, k, name) for k, name in hit)
    if not files:
        return None
    if rule == "variants":
        decls = [d for d in decls if d[1] == "enum" and gained_variant(repo, parent, sha, d[0], d[2])]
        files = sorted({d[0] for d in decls})
        if not files:
            return None
    if rule in ("users", "variants"):
        # A file that both defines and uses is applied, so it can't be the user that
        # breaks. Drop the definers whose names nobody else edited, then look again.
        while True:
            users = edited_users(repo, lang, sha, parent, status, {d[2] for d in decls}, set(files))
            keep = {d[0] for d in decls if d[2] in users}
            if keep == set(files):
                break
            files = [f for f in files if f in keep]
            decls = [d for d in decls if d[0] in keep]
            if not files:
                return None
        decls = [d for d in decls if d[2] in users]
        return {"sha": sha, "parent": parent, "files": files, "decls": [f"{p}: {k} {n}" for p, k, n in decls],
                "users": {n: sorted(u) for n, u in sorted(users.items()) if any(d[2] == n for d in decls)},
                "files_in_commit": len(status)}
    return {"sha": sha, "parent": parent, "files": files, "decls": [f"{p}: {k} {n}" for p, k, n in decls],
            "files_in_commit": len(status)}


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--repo", required=True, type=Path)
    ap.add_argument("--lang", required=True, choices=sorted(EXTS))
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--head", default="HEAD")
    ap.add_argument("--count", type=int, default=20, help="candidates to keep, in order; the driver takes the first that build")
    ap.add_argument("--scan", type=int, default=400, help="commits to look at from HEAD back")
    ap.add_argument("--max-files", type=int, default=30)
    ap.add_argument("--rule", choices=["declaration", "users", "variants", "arms"], default="declaration",
                    help="users: the commit also edits a file that names the declaration; "
                         "variants: users, for a Rust enum that gained a variant; "
                         "arms: a Rust enum that gained a variant, with Self::, glob or nested arms over it")
    a = ap.parse_args()

    head = git(a.repo, "rev-parse", a.head).strip()
    log = git(a.repo, "log", "--no-merges", f"-n{a.scan}", "--format=%H %P", head).splitlines()
    picked = []
    for row in log:
        parts = row.split()
        if len(parts) != 2:
            continue
        c = candidate(a.repo, a.lang, parts[0], parts[1], a.max_files, a.rule)
        if c:
            picked.append(c)
            if len(picked) >= a.count:
                break
    a.out.write_text(json.dumps({"repo": str(a.repo.resolve()), "lang": a.lang, "head": head,
                                 "scanned": len(log), "rule_name": a.rule,
                                 "rule": __doc__.split("\n\n")[{"declaration": 1, "users": 4, "variants": 5, "arms": 6}[a.rule]],
                                 "sample": picked}, indent=1) + "\n")
    print(f"{a.repo.name}: {len(log)} commits scanned, {len(picked)} candidates -> {a.out}")


if __name__ == "__main__":
    main()
