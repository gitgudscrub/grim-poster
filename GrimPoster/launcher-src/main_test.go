package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func makeZip(name string) []byte {
	b := new(bytes.Buffer)
	z := zip.NewWriter(b)
	w, _ := z.Create(name)
	w.Write([]byte("test"))
	z.Close()
	return b.Bytes()
}
func TestExtract(t *testing.T) {
	d := t.TempDir()
	if err := unzipBundle(makeZip("desktop/test.txt"), d, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(d, "desktop", "test.txt"))
	if err != nil || string(b) != "test" {
		t.Fatalf("unexpected extract: %v %s", err, b)
	}
}
func TestPreserve(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "desktop", "test.txt")
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte("newer"), 0600)
	if err := unzipBundle(makeZip("desktop/test.txt"), d, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "newer" {
		t.Fatalf("overwrote newer file: %s", b)
	}
}
func TestRejectTraversal(t *testing.T) {
	d := t.TempDir()
	for _, path := range []string{"../oops", "desktop/../../../oops", "unauthorized/file"} {
		if err := unzipBundle(makeZip(path), d, false); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
}
func TestVersionPreservation(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "patch", "patch-manifest.json")
	os.MkdirAll(filepath.Dir(p), 0700)
	for _, v := range []struct {
		v     string
		newer bool
	}{{"0.1.47", false}, {"0.1.48", false}, {"0.1.49", false}, {"0.1.50", false}, {"0.1.64", false}, {"0.1.65", false}, {"0.1.66", false}, {"0.1.67", false}, {"0.1.68", true}, {"0.2.0", true}} {
		os.WriteFile(p, []byte(`{"modVersion":"`+v.v+`"}`), 0600)
		if actual := newerInstalledPatch(d); actual != v.newer {
			t.Errorf("version %s got %v", v.v, actual)
		}
	}
}

func TestManagerCanUpgradeWhenPatchPreserved(t *testing.T) {
	d := t.TempDir()
	manager := filepath.Join(d, "desktop", "Start-GrimPosterManager.ps1")
	patch := filepath.Join(d, "desktop", "patch", "patch-manifest.json")
	os.MkdirAll(filepath.Dir(patch), 0700)
	os.WriteFile(manager, []byte("old manager"), 0600)
	os.WriteFile(patch, []byte("new signed patch"), 0600)
	b := new(bytes.Buffer)
	z := zip.NewWriter(b)
	for _, x := range []string{"desktop/Start-GrimPosterManager.ps1", "desktop/patch/patch-manifest.json"} {
		w, _ := z.Create(x)
		w.Write([]byte("bundled payload"))
	}
	z.Close()
	if err := unzipBundle(b.Bytes(), d, true); err != nil {
		t.Fatal(err)
	}
	m, _ := os.ReadFile(manager)
	p, _ := os.ReadFile(patch)
	if string(m) != "bundled payload" {
		t.Fatal("manager wasn't upgraded")
	}
	if string(p) != "new signed patch" {
		t.Fatal("signed patch was overwritten")
	}
}
