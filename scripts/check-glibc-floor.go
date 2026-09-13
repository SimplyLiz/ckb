//go:build ignore

// check-glibc-floor.go fails if a linux binary asks the dynamic loader for a
// newer glibc than we promise to support.
//
// This exists because of #243. The release matrix builds the linux targets with
// cgo on GitHub-hosted runners, and the glibc version a binary ends up demanding
// is decided by the *runner image*, not by anything in this repo: the linker
// stamps every undefined libc symbol with the version the build host's libc
// happens to define it at. When `ubuntu-latest` migrated 22.04 -> 24.04, two
// weak symbols Rust's std references for its posix_spawn fast path
// (pidfd_spawnp, pidfd_getpid) started resolving against glibc 2.39, which put a
// GLIBC_2.39 entry in .gnu.version_r. The loader enforces that entry before the
// process starts, so 9.3.0 could not run on any distro older than Ubuntu 24.04 —
// no code change, no build failure, no warning.
//
// The check reads .gnu.version_r, which is exactly what the loader enforces.
// Weak symbols are reported for diagnosis but are not exempt: a weak symbol that
// the linker *did* resolve still contributes a hard version requirement.
//
// Usage:
//
//	go run scripts/check-glibc-floor.go --max 2.34 ./ckb [more binaries...]
package main

import (
	"debug/elf"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	max := flag.String("max", "2.34", "highest glibc version the binary may require (e.g. 2.34)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: check-glibc-floor.go --max 2.34 <binary> [binary...]")
		os.Exit(2)
	}

	ceiling, err := parseVersion("GLIBC_" + *max)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --max %q: %v\n", *max, err)
		os.Exit(2)
	}

	failed := false
	for _, path := range flag.Args() {
		if err := checkFile(path, *max, ceiling); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func checkFile(path, maxLabel string, ceiling []int) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%s: not a readable ELF binary: %v", path, err)
	}
	defer f.Close()

	needs, err := f.DynamicVersionNeeds()
	if err != nil {
		// A statically linked binary has no version requirements at all, which
		// is the strongest possible result — nothing to enforce.
		fmt.Printf("%s: statically linked, no glibc requirement\n", path)
		return nil
	}

	// Collect every GLIBC_* version the loader will enforce, and remember which
	// undefined symbols carry each one so a failure names names.
	offenders := map[string][]string{}
	worst := ""
	var worstV []int
	for _, need := range needs {
		for _, dep := range need.Needs {
			if !strings.HasPrefix(dep.Dep, "GLIBC_") {
				continue // GCC_*, etc. — not glibc, not our floor
			}
			v, err := parseVersion(dep.Dep)
			if err != nil {
				return fmt.Errorf("%s: cannot parse version %q: %v", path, dep.Dep, err)
			}
			if compare(v, ceiling) > 0 {
				offenders[dep.Dep] = nil
			}
			if worstV == nil || compare(v, worstV) > 0 {
				worst, worstV = dep.Dep, v
			}
		}
	}

	if len(offenders) > 0 {
		syms, _ := f.DynamicSymbols()
		for _, s := range syms {
			if s.Section != elf.SHN_UNDEF {
				continue
			}
			if _, over := offenders[s.Version]; !over {
				continue
			}
			label := s.Name
			if elf.ST_BIND(s.Info) == elf.STB_WEAK {
				label += " (weak)"
			}
			offenders[s.Version] = append(offenders[s.Version], label)
		}

		var vers []string
		for v := range offenders {
			vers = append(vers, v)
		}
		sort.Strings(vers)

		var b strings.Builder
		fmt.Fprintf(&b, "%s: requires glibc newer than the %s floor\n", path, maxLabel)
		for _, v := range vers {
			sort.Strings(offenders[v])
			fmt.Fprintf(&b, "    %s pulled in by: %s\n", v, strings.Join(offenders[v], ", "))
		}
		b.WriteString("  A \"(weak)\" symbol is an optional fast path the code guards at runtime,\n")
		b.WriteString("  but the loader still refuses to start the process over the version entry.\n")
		b.WriteString("  Build this target on a runner whose glibc is at or below the floor —\n")
		b.WriteString("  the symbol then does not exist to link against and drops out entirely.")
		return fmt.Errorf("%s", b.String())
	}

	fmt.Printf("%s: OK — highest requirement %s, floor GLIBC_%s\n", path, worst, maxLabel)
	return nil
}

// parseVersion turns "GLIBC_2.34" into [2 34] so versions compare numerically;
// "GLIBC_2.4" must sort below "GLIBC_2.34", which a string compare gets wrong.
func parseVersion(s string) ([]int, error) {
	raw := strings.TrimPrefix(s, "GLIBC_")
	parts := strings.Split(raw, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("%q is not numeric", p)
		}
		out[i] = n
	}
	return out, nil
}

func compare(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}
