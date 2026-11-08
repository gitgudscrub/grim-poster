package main

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOfficialRelease(t *testing.T) {
	v := "3.58.0"
	name := "Blood.on.the.Clocktower.Online_" + v + "_x64-setup.exe"
	payload := map[string]any{"tag_name": "v" + v, "assets": []map[string]any{{"name": name, "size": 39641384, "digest": "sha256:" + strings.Repeat("a", 64), "browser_download_url": "https://github.com/ThePandemoniumInstitute/botc-release/releases/download/v" + v + "/" + name}}}
	raw, _ := json.Marshal(payload)
	r, e := parseOfficial(raw)
	if e != nil || r.Version != v {
		t.Fatalf("valid release rejected: %v", e)
	}
	payload["assets"] = []map[string]any{{"name": name, "size": 39641384, "digest": "sha256:" + strings.Repeat("a", 64), "browser_download_url": "https://evil.example.com/installer.exe"}}
	raw, _ = json.Marshal(payload)
	if _, e := parseOfficial(raw); e == nil {
		t.Fatal("untrusted installer URL accepted")
	}
}
func TestSignedManifestIsNotTrustedWithoutSignature(t *testing.T) {
	sig := make([]byte, 512)
	rand.Read(sig)
	if err := checkPublicSignature([]byte(`{"modVersion":"0.1.55"}`), sig); err == nil {
		t.Fatal("invalid signature passed")
	}
}
func TestReadyForAutoLaunch(t *testing.T) {
	patched := "abc"
	s := stateSnapshot{Installed: &installedBOTC{Version: "3.58.0", SHA256: patched}, Official: &officialRelease{Version: "3.58.0"}, Patch: patchManifest{AppVersion: "3.58.0", ModVersion: "0.1.49", ExpectedPatchedSha256: patched}, Remote: &remoteStatus{Compatible: true, PatchInstalled: true}}
	if !s.ready() {
		t.Fatal("matching app and patch should auto-launch")
	}
	s.Remote.UpdateAvailable = true
	if s.ready() {
		t.Fatal("pending signed update should block automatic launch")
	}
	s.Remote.UpdateAvailable = false
	s.Installed.Version = "3.57.3"
	if s.ready() {
		t.Fatal("obsolete game should block automatic launch")
	}
}
func TestVersionOrdering(t *testing.T) {
	for _, q := range []struct {
		a, b string
		want int
	}{{"0.1.55", "0.1.54", 1}, {"3.58.0", "3.58.0", 0}, {"3.9.0", "3.58.0", -1}} {
		if v := versionCmp(q.a, q.b); v != q.want {
			t.Errorf("compare %s %s: got %d", q.a, q.b, v)
		}
	}
}

// An incomplete locally bundled beta patch must not suppress the older, valid
// signed production patch or advertise an unsafe Install/Repair action.
func TestIncompleteLocalPatchFallsBackToSignedRelease(t *testing.T) {
	desktop := t.TempDir()
	if err := os.MkdirAll(filepath.Join(desktop, "patch"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"appVersion":"3.58.0","modVersion":"0.1.50","expectedPatchedSha256":""}`)
	if err := os.WriteFile(filepath.Join(desktop, "patch", "patch-manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := readPatch(desktop)
	if err == nil {
		t.Fatal("incomplete manifest accepted")
	}
	if p.AppVersion != "" || p.ModVersion != "" {
		t.Fatalf("incomplete manifest leaked version: %#v", p)
	}
	original := &installedBOTC{Version: "3.58.0", SHA256: strings.Repeat("a", 64)}
	signed := &signedRelease{ModVersion: "0.1.49", AppVersion: "3.58.0"}
	remote := makeRemote(signed, original, p)
	if !remote.Compatible || !remote.UpdateAvailable {
		t.Fatalf("signed fallback not offered: %#v", remote)
	}
	snap := stateSnapshot{Installed: original, Patch: p, Remote: remote}
	if snap.canUseLocalPatch() {
		t.Fatal("incomplete patch offered for local installation")
	}
}
