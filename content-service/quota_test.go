package main

import (
	"regexp"
	"testing"
	"time"
)

func TestUsagePeriodFormat(t *testing.T) {
	p := usagePeriod()
	if !regexp.MustCompile(`^\d{4}-\d{2}$`).MatchString(p) {
		t.Fatalf("usagePeriod = %q, want YYYY-MM", p)
	}
}

func TestMonthEndIsFirstOfNextMonth(t *testing.T) {
	e := monthEnd()
	if e.Day() != 1 || e.Hour() != 0 || e.Location() != time.UTC {
		t.Fatalf("monthEnd = %v, want 1st of next month 00:00 UTC", e)
	}
	if !e.After(time.Now().UTC()) {
		t.Fatalf("monthEnd %v should be in the future", e)
	}
}

func TestPauseAheadDefault(t *testing.T) {
	t.Setenv("PAUSE_AHEAD_PAGES", "")
	if pauseAheadPages() != 60 {
		t.Fatalf("default pauseAheadPages = %d, want 60", pauseAheadPages())
	}
	t.Setenv("PAUSE_AHEAD_PAGES", "20")
	if pauseAheadPages() != 20 {
		t.Fatalf("pauseAheadPages with env = %d, want 20", pauseAheadPages())
	}
}
