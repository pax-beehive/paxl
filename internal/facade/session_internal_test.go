package facade

import (
	"testing"
	"time"
)

func TestSanitizeFTSQueryPreservesPhrasesAndTrimsOperators(t *testing.T) {
	got := sanitizeFTSQuery(`AND docker+(deploy) OR "exact phrase" NOT`)
	want := `docker  deploy  OR "exact phrase"`
	if got != want {
		t.Fatalf("sanitizeFTSQuery() = %q, want %q", got, want)
	}
}

func TestRecentlySyncedHandlesEmptyInvalidFreshAndStaleValues(t *testing.T) {
	if recentlySynced("") {
		t.Fatalf("recentlySynced() should be false for empty input")
	}
	if recentlySynced("not-a-time") {
		t.Fatalf("recentlySynced() should be false for invalid input")
	}
	if !recentlySynced(time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)) {
		t.Fatalf("recentlySynced() should be true for fresh input")
	}
	if recentlySynced(time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)) {
		t.Fatalf("recentlySynced() should be false for stale input")
	}
}
