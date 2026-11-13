package main

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveReviewTarget(t *testing.T) {
	prRefs := func(pr string) (string, string, error) {
		switch pr {
		case "42":
			return "develop", "no-such-local-branch-xyz", nil
		case "7":
			return "main", "", nil
		}
		return "", "", errors.New("no such PR")
	}

	cases := []struct {
		target string
		want   reviewTargetSpec
	}{
		{"", reviewTargetSpec{}},
		{"  ", reviewTargetSpec{}},
		{"staged", reviewTargetSpec{Staged: true}},
		{"develop", reviewTargetSpec{Base: "develop"}},
		{"origin/main", reviewTargetSpec{Base: "origin/main"}},
		{"release-2", reviewTargetSpec{Base: "release-2"}},
	}
	for _, c := range cases {
		got, err := resolveReviewTarget(c.target, prRefs)
		if err != nil || got != c.want {
			t.Errorf("resolveReviewTarget(%q) = %+v, %v; want %+v", c.target, got, err, c.want)
		}
	}

	// A PR whose branch is not checked out here: its base, the current
	// checkout as head, and a note that says so.
	for _, target := range []string{"42", "#42"} {
		got, err := resolveReviewTarget(target, prRefs)
		if err != nil || got.Base != "develop" || got.Head != "" || !strings.Contains(got.Note, "gh pr checkout 42") {
			t.Errorf("resolveReviewTarget(%q) = %+v, %v", target, got, err)
		}
	}

	if _, err := resolveReviewTarget("999", prRefs); err == nil || !strings.Contains(err.Error(), "PR 999") {
		t.Errorf("unknown PR: err = %v", err)
	}
	if _, err := resolveReviewTarget("main extra", prRefs); err == nil {
		t.Error("two words: want an error, $ARGUMENTS with spaces must not pass as a branch")
	}
}
