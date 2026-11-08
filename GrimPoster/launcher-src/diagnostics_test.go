package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiagnosticsDoNotSendRawError(t *testing.T) {
	r := newRecord("patch_install_failed", errors.New(`open C:\Users\Alice\Documents\webhook-secret.txt: access is denied`))
	if r.Category != "permission_denied" {
		t.Fatalf("wrong category: %#v", r)
	}
	if r.Event != "patch_install_failed" || r.Version != bootstrapVersion {
		t.Fatal("missing known metadata")
	}
	b := safeRecord(r)
	if b.Event != r.Event || b.Category != r.Category {
		t.Fatal("sanitization altered allowed fields")
	}
}
func TestLatestErrorNotStartup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.jsonl")
	old := newRecord("operation_failed", errors.New("bad"))
	old.Time = time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	for _, r := range []diagnosticRecord{old, newRecord("patch_install_failed", os.ErrPermission), newRecord("launcher_start", nil)} {
		if err := appendRecord(p, r); err != nil {
			t.Fatal(err)
		}
	}
	r, e := lastErrorRecord(p)
	if e != nil {
		t.Fatal(e)
	}
	if r.Event != "patch_install_failed" || r.Category != "permission_denied" {
		t.Fatalf("wrong report %#v", r)
	}
}
func TestNoReportWithoutFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.jsonl")
	_ = appendRecord(p, newRecord("launcher_start", nil))
	if _, e := lastErrorRecord(p); e == nil {
		t.Fatal("unexpected report")
	}
}
