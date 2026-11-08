package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessRole(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{{"--companion", "companion"}, {"--native-manager", "native_manager"}, {"--manager", "launcher"}} {
		if got := processRole([]string{"GrimPoster", tc.arg}); got != tc.want {
			t.Fatalf("%s got %s", tc.arg, got)
		}
	}
}
func TestCrashMarkerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	m := crashMarker{Version: bootstrapVersion, Role: "native_manager", PID: 42, Started: "2026-10-24T07:00:00Z", Stage: "creating_native_window"}
	if err := writeMarker(path, m); err != nil {
		t.Fatal(err)
	}
	var got crashMarker
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(b, &got) != nil || got.Stage != m.Stage {
		t.Fatalf("bad marker %s", b)
	}
}
func TestRecoverAbandonedProcess(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("LOCALAPPDATA", temp)
	wasPreviousCrash = false
	m := crashMarker{Version: bootstrapVersion, Role: "companion", PID: 99999998, Stage: "capturing_grimoire", Started: time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339), Updated: time.Now().Add(-9 * time.Minute).UTC().Format(time.RFC3339)}
	path := filepath.Join(sessionDir(), "session-99999998.json")
	if err := writeMarker(path, m); err != nil {
		t.Fatal(err)
	}
	recoverAbandonedSessions()
	if !wasPreviousCrash {
		t.Fatal("crash not detected")
	}
	b, err := os.ReadFile(filepath.Join(diagnosticDirectory(), "last-unexpected-exit.txt"))
	if err != nil || !strings.Contains(string(b), "capturing_grimoire") {
		t.Fatalf("no summary: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale marker not consumed")
	}
	wasPreviousCrash = false
	recoverAbandonedSessions()
	if wasPreviousCrash {
		t.Fatal("duplicate crash")
	}
}
func TestReportSelectsLastFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "diagnostics.jsonl")
	_ = appendRecord(p, newRecord("capture_failed", os.ErrPermission))
	_ = appendRecord(p, newRecord("launcher_start", nil))
	r, err := lastErrorRecord(p)
	if err != nil || r.Event != "capture_failed" {
		t.Fatalf("selected %v %v", r, err)
	}
	if safeRecord(r).Event != "other_event" {
		t.Fatal("should map unsupported server events to other_event")
	}
}
