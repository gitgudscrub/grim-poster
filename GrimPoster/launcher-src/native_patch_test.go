package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func syntheticPatch(t *testing.T) (string, string, nativePatchManifest, []byte) {
	t.Helper()
	d := t.TempDir()
	root := filepath.Join(d, "release")
	os.MkdirAll(filepath.Join(root, "patch"), 0700)
	exe := filepath.Join(d, "botc-app.exe")
	base := make([]byte, 8192)
	for i := range base {
		base[i] = byte((i*37 + 17) % 256)
	}
	p := nativePatchManifest{FormatVersion: 1, AppVersion: "3.58.0", ModVersion: "0.1.56", ExpectedOriginalSHA: hashBytes(base), AssetDataOffset: 512, AssetCapacity: 200, PatchDataLength: 10, DataLengthOffsets: []int{4088}}
	payload := []byte("patch-data")
	p.PatchDataSHA = hashBytes(payload)
	newBytes := append([]byte(nil), base...)
	for i := p.AssetDataOffset; i < p.AssetDataOffset+p.AssetCapacity; i++ {
		newBytes[i] = 0
	}
	copy(newBytes[p.AssetDataOffset:], payload)
	binary.LittleEndian.PutUint64(newBytes[p.DataLengthOffsets[0]:], uint64(len(payload)))
	p.ExpectedPatchedSHA = hashBytes(newBytes)
	os.WriteFile(exe, base, 0600)
	jsonData, _ := json.Marshal(p)
	os.WriteFile(filepath.Join(root, "patch", "patch-manifest.json"), jsonData, 0600)
	os.WriteFile(filepath.Join(root, "patch", "grim-poster-main.br"), payload, 0600)
	return exe, root, p, base
}
func TestNativePatchInstallAndVerifiedBackup(t *testing.T) {
	exe, root, p, _ := syntheticPatch(t)
	if err := installPatchAt(exe, root, "3.58.0", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := hashFile(exe); got != p.ExpectedPatchedSHA {
		t.Fatalf("unexpected patch SHA: %s", got)
	}
	if got, _ := hashFile(exe + ".grim-poster-original"); got != p.ExpectedOriginalSHA {
		t.Fatalf("wrong recovery backup: %s", got)
	}
	if err := installPatchAt(exe, root, "3.58.0", ""); err != nil {
		t.Fatal("idempotency:", err)
	}
}
func TestNativePatchRejectUnknownExecutable(t *testing.T) {
	exe, root, _, _ := syntheticPatch(t)
	b, _ := os.ReadFile(exe)
	b[2000] ^= 0xff
	os.WriteFile(exe, b, 0600)
	if err := installPatchAt(exe, root, "3.58.0", ""); err == nil {
		t.Fatal("unexpectedly patched unknown executable")
	}
	if _, err := os.Stat(exe + ".grim-poster-original"); err == nil {
		t.Fatal("created backup for unknown executable")
	}
	if got, _ := hashFile(exe); got != hashBytes(b) {
		t.Fatal("modified unknown executable")
	}
}
func TestNativePatchRejectUnsupportedVersion(t *testing.T) {
	exe, root, _, b := syntheticPatch(t)
	if err := installPatchAt(exe, root, "3.59.0", ""); err == nil {
		t.Fatal("accepted incompatible version")
	}
	if got, _ := hashFile(exe); got != hashBytes(b) {
		t.Fatal("modified executable")
	}
}
func TestNativePatchRejectTamperedPayload(t *testing.T) {
	exe, root, _, b := syntheticPatch(t)
	os.WriteFile(filepath.Join(root, "patch", "grim-poster-main.br"), []byte("bad-payload"), 0600)
	if err := installPatchAt(exe, root, "3.58.0", ""); err == nil {
		t.Fatal("accepted tampered payload")
	}
	if got, _ := hashFile(exe); got != hashBytes(b) {
		t.Fatal("modified executable")
	}
}

func TestDerivedTargetHashRebuildAndUninstall(t *testing.T) {
	exe, root, p, base := syntheticPatch(t)
	p.ExpectedPatchedSHA = ""
	p.PatchVerification = "derive-from-verified-original"
	b, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(root, "patch", "patch-manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readNativePatch(root); err != nil {
		t.Fatalf("derived mode wrongly rejected: %v", err)
	}
	if err := installPatchAt(exe, root, p.AppVersion, ""); err != nil {
		t.Fatal(err)
	}
	payload, _ := os.ReadFile(filepath.Join(root, "patch", "grim-poster-main.br"))
	wanted, err := deriveTargetSHA(exe, payload, p)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := hashFile(exe)
	if actual != wanted {
		t.Fatalf("derived digest mismatch got %s wanted %s", actual, wanted)
	}
	result, err := restoreBotcPatch(exe, root, p.AppVersion)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !result.Restored {
		t.Fatal("patched game not restored")
	}
	current, _ := os.ReadFile(exe)
	if hashBytes(current) != hashBytes(base) {
		t.Fatal("uninstaller did not restore exact verified official bytes")
	}
}
func TestDerivedTargetWithoutOfficialBackupFailsClosed(t *testing.T) {
	exe, root, p, base := syntheticPatch(t)
	p.ExpectedPatchedSHA = ""
	p.PatchVerification = "derive-from-verified-original"
	b, _ := json.Marshal(p)
	os.WriteFile(filepath.Join(root, "patch", "patch-manifest.json"), b, 0600)
	raw := append([]byte(nil), base...)
	raw[400] ^= 0xf3
	os.WriteFile(exe, raw, 0600)
	if err := installPatchAt(exe, root, p.AppVersion, ""); err == nil {
		t.Fatal("accepted unknown executable without verified original")
	}
	check, _ := os.ReadFile(exe)
	if hashBytes(check) != hashBytes(raw) {
		t.Fatal("unknown executable changed")
	}
}
func TestDerivedTargetModeMustBeExplicit(t *testing.T) {
	exe, root, p, _ := syntheticPatch(t)
	p.ExpectedPatchedSHA = ""
	raw, _ := json.Marshal(p)
	os.WriteFile(filepath.Join(root, "patch", "patch-manifest.json"), raw, 0600)
	if _, _, err := readNativePatch(root); err == nil {
		t.Fatal("unsigned incomplete target hash accepted without explicit verification mode")
	}
	if err := installPatchAt(exe, root, p.AppVersion, ""); err == nil {
		t.Fatal("invalid manifest installed")
	}
}
