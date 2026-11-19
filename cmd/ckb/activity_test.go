package main

import (
	"math"
	"testing"
	"time"
)

func TestParseDurationWithDays(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"2h", 2 * time.Hour},
		{"30m", 30 * time.Minute},
		{"7d", 7 * 24 * time.Hour},
		{"1.5d", 36 * time.Hour},
	}
	for _, tc := range cases {
		got, err := parseDurationWithDays(tc.in)
		if err != nil {
			t.Errorf("parseDurationWithDays(%q) returned error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDurationWithDays(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseDurationWithDaysInvalid(t *testing.T) {
	if _, err := parseDurationWithDays("xd"); err == nil {
		t.Errorf("expected error for invalid days value")
	}
	if _, err := parseDurationWithDays("not-a-duration"); err == nil {
		t.Errorf("expected error for garbage input")
	}
}

func TestParseActivitySinceEmpty(t *testing.T) {
	ms, err := parseActivitySince("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ms != 0 {
		t.Errorf("expected 0 for empty since, got %d", ms)
	}
}

func TestParseActivitySinceComputesPastCutoff(t *testing.T) {
	before := time.Now().Add(-2 * time.Hour).UnixMilli()
	ms, err := parseActivitySince("2h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := time.Now().Add(-2 * time.Hour).UnixMilli()

	if ms < before-1000 || ms > after+1000 {
		t.Errorf("expected cutoff near %d..%d, got %d", before, after, ms)
	}
}

func TestParseActivitySinceInvalid(t *testing.T) {
	if _, err := parseActivitySince("banana"); err == nil {
		t.Errorf("expected error for invalid --since value")
	}
}

func TestFormatFactsSortedDeterministic(t *testing.T) {
	facts := map[string]int{"tests": 3, "dependents": 27, "cochange": 6}
	got := formatFacts(facts)
	want := "cochange 6 · dependents 27 · tests 3"
	if got != want {
		t.Errorf("formatFacts = %q, want %q", got, want)
	}
}

func TestFormatFactsEmpty(t *testing.T) {
	if got := formatFacts(map[string]int{}); got != "" {
		t.Errorf("expected empty string for empty facts, got %q", got)
	}
}

func TestFormatActivityBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{6554, "6.4 KB"},
	}
	for _, tc := range cases {
		if got := formatActivityBytes(tc.in); got != tc.want {
			t.Errorf("formatActivityBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRepoDisplayName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/Users/lisa/Work/Projects/CKB/src", "src"},
		{"/Users/lisa/Work/Projects/CKB/src/", "src"},
		{"relative", "relative"},
	}
	for _, tc := range cases {
		if got := repoDisplayName(tc.in); got != tc.want {
			t.Errorf("repoDisplayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDisplayOrDash(t *testing.T) {
	if got := displayOrDash(""); got != "-" {
		t.Errorf("expected dash for empty string, got %q", got)
	}
	if got := displayOrDash("claude-code"); got != "claude-code" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestParseDurationWithDaysFractional(t *testing.T) {
	got, err := parseDurationWithDays("0.5d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 12 * time.Hour
	if math.Abs(float64(got-want)) > float64(time.Second) {
		t.Errorf("parseDurationWithDays(0.5d) = %v, want ~%v", got, want)
	}
}
