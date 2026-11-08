package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallRestoresOnlyVerifiedOriginal(t *testing.T) {
	exe, release, p, base := syntheticPatch(t)
	if err := installPatchAt(exe, release, p.AppVersion, ""); err != nil {
		t.Fatal(err)
	}
	r, err := restoreBotcPatch(exe, release, p.AppVersion)
	if err != nil || !r.Restored {
		t.Fatalf("restore: %+v: %v", r, err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != string(base) {
		t.Fatal("game was not restored byte for byte")
	}
	if _, err := os.Stat(exe + ".grim-poster-original"); !os.IsNotExist(err) {
		t.Fatal("verified backup was not removed")
	}
	r, err = restoreBotcPatch(exe, release, p.AppVersion)
	if err != nil || !r.AlreadyOfficial {
		t.Fatalf("idempotency: %+v %v", r, err)
	}
}
func TestUninstallRejectsMissingBackup(t *testing.T) {
	exe, release, p, _ := syntheticPatch(t)
	if err := installPatchAt(exe, release, p.AppVersion, ""); err != nil {
		t.Fatal(err)
	}
	os.Remove(exe + ".grim-poster-original")
	before, _ := hashFile(exe)
	if _, err := restoreBotcPatch(exe, release, p.AppVersion); err == nil {
		t.Fatal("accepted missing backup")
	}
	after, _ := hashFile(exe)
	if before != after {
		t.Fatal("changed game with missing backup")
	}
}
func TestUninstallRejectsUnknownGameAndBackup(t *testing.T) {
	for _, poison := range []string{"game", "backup"} {
		exe, release, p, _ := syntheticPatch(t)
		if err := installPatchAt(exe, release, p.AppVersion, ""); err != nil {
			t.Fatal(err)
		}
		f := exe
		if poison == "backup" {
			f = exe + ".grim-poster-original"
		}
		b, _ := os.ReadFile(f)
		b[100] ^= 42
		os.WriteFile(f, b, 0600)
		before, _ := hashFile(exe)
		if _, err := restoreBotcPatch(exe, release, p.AppVersion); err == nil {
			t.Fatalf("accepted corrupted %s", poison)
		}
		after, _ := hashFile(exe)
		if before != after {
			t.Fatal("modified unrecognized game")
		}
	}
}
func TestUninstallRejectsWrongVersionAndUnknownName(t *testing.T) {
	exe, release, p, _ := syntheticPatch(t)
	if _, err := restoreBotcPatch(exe, release, "3.59.0"); err == nil {
		t.Fatal("wrong version allowed")
	}
	if _, err := restoreBotcPatch(filepath.Join(t.TempDir(), "other.exe"), release, p.AppVersion); err == nil {
		t.Fatal("unknown exe name allowed")
	}
}
func TestUninstallWithoutGame(t *testing.T) {
	r, err := restoreBotcPatch("", "", "")
	if err != nil || !r.GameNotFound {
		t.Fatalf("no-game uninstall: %+v, %v", r, err)
	}
}

func TestRemoveManagedSupportPreservesUserData(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "desktop", "patch"), 0700)
	os.MkdirAll(filepath.Join(root, "logs"), 0700)
	os.WriteFile(filepath.Join(root, "desktop", "patch", "file"), []byte("gp"), 0600)
	os.WriteFile(filepath.Join(root, "logs", "important.log"), []byte("evidence"), 0600)
	os.WriteFile(filepath.Join(root, "user-screenshot.png"), []byte("keep"), 0600)
	os.WriteFile(filepath.Join(root, "bootstrap-version.txt"), []byte("0.1.60"), 0600)
	if _, err := removeManagedSupport(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "desktop")); !os.IsNotExist(err) {
		t.Fatal("desktop support retained")
	}
	for _, name := range []string{"logs/important.log", "user-screenshot.png"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("removed user data %s: %v", name, err)
		}
	}
}
