package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdaptivePolicy(t *testing.T) {
	s := adaptiveSettings{Preference: modeAdaptive, AppVersion: "3.58.0", NativeTrouble: true}
	m, _ := chosenMode(s, "3.58.0", true)
	if m != modeLegacy {
		t.Fatal(m)
	}
	m, _ = chosenMode(s, "3.58.0", false)
	if m != modeNative {
		t.Fatal(m)
	}
	m, _ = chosenMode(s, "3.59.0", true)
	if m != modeNative {
		t.Fatal(m)
	}
	s.Preference = modeLegacy
	m, _ = chosenMode(s, "3.58.0", false)
	if m != "" {
		t.Fatal(m)
	}
	s.Preference = modeNative
	m, _ = chosenMode(s, "3.58.0", true)
	if m != modeNative {
		t.Fatal(m)
	}
}
func TestPauseWhenFallbackBlocked(t *testing.T) {
	s := adaptiveSettings{Preference: modeAdaptive, AppVersion: "3.58.0", NativeTrouble: true}
	if safeToAutoLaunch(s, "3.58.0", false) {
		t.Fatal("unsafe automatic launch")
	}
	if !safeToAutoLaunch(s, "3.58.0", true) {
		t.Fatal("legacy should be usable")
	}
	if !safeToAutoLaunch(s, "3.59.0", false) {
		t.Fatal("old warning should not block new app")
	}
	s.Preference = modeNative
	if !safeToAutoLaunch(s, "3.58.0", false) {
		t.Fatal("manual force native blocked")
	}
}
func TestAdaptivePersistence(t *testing.T) {
	root := t.TempDir()
	if err := setPreference(root, modeLegacy); err != nil {
		t.Fatal(err)
	}
	s := loadAdaptive(root)
	if s.Preference != modeLegacy {
		t.Fatal(s)
	}
	b, e := os.ReadFile(filepath.Join(root, "adaptive-settings.json"))
	if e != nil || len(b) == 0 {
		t.Fatalf("missing file %v", e)
	}
	if nextMode(modeLegacy) != modeAdaptive || nextMode(modeAdaptive) != modeNative {
		t.Fatal("mode cycle")
	}
}

func TestBundledCompatibilityScripts(t *testing.T) {
	root := t.TempDir()
	if err := unzipBundle(bundled, root, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"patch/patch-manifest.json"} {
		b, e := os.ReadFile(filepath.Join(root, "desktop", name))
		if e != nil || len(b) < 100 {
			t.Fatalf("missing compatibility script %s: %v", name, e)
		}
	}
}
