package main

// Native patch installer for environments in which PowerShell scripts are disabled.
// Only applies payloads whose full resulting executable hash matches the bundled,
// signed release's target hash. Preserves an exact official-executable backup.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type patchPayload struct {
	Version string `json:"version"`
	File    string `json:"file"`
	Length  int    `json:"length"`
	SHA256  string `json:"sha256"`
}
type nativePatchManifest struct {
	FormatVersion                int            `json:"formatVersion"`
	AppVersion                   string         `json:"appVersion"`
	ModVersion                   string         `json:"modVersion"`
	ExpectedOriginalSHA          string         `json:"expectedOriginalSha256"`
	ExpectedPatchedSHA           string         `json:"expectedPatchedSha256"`
	PatchVerification            string         `json:"patchVerification"`
	SupportedUpgradeSHA          []string       `json:"supportedUpgradeSha256"`
	SupportedPreviousOfficialSHA []string       `json:"supportedPreviousOfficialSha256"`
	AssetDataOffset              int            `json:"assetDataOffset"`
	AssetCapacity                int            `json:"assetCapacity"`
	DataLengthOffsets            []int          `json:"dataLengthOffsets"`
	PatchDataLength              int            `json:"patchDataLength"`
	PatchDataSHA                 string         `json:"patchDataSha256"`
	SupportedPatchPayloads       []patchPayload `json:"supportedPatchPayloads"`
}

// A bundled, *unsigned beta* may reconstruct its exact target digest from a
// separately verified official BOTC binary and a separately verified payload.
// Officially signed update packages still require a fixed target SHA-256.
func validTargetSHA(p nativePatchManifest) bool {
	return shaRE.MatchString(p.ExpectedPatchedSHA) ||
		(p.ExpectedPatchedSHA == "" && p.PatchVerification == "derive-from-verified-original")
}

// deriveTargetSHA computes, rather than guesses, the full patched executable
// hash. Only the verified official BOTC binary can be its input. It never
// accepts a previously modified executable as a substitute for that original.
func deriveTargetSHA(exe string, payload []byte, p nativePatchManifest) (string, error) {
	for _, candidate := range []string{exe + ".grim-poster-original", exe} {
		base, err := os.ReadFile(candidate)
		if err != nil || !strings.EqualFold(hashBytes(base), p.ExpectedOriginalSHA) {
			continue
		}
		rebuilt, err := patchedBytes(base, payload, p)
		if err != nil {
			return "", err
		}
		return hashBytes(rebuilt), nil
	}
	return "", errors.New("verified official BOTC executable or recovery copy is required to calculate patch SHA-256")
}

func readNativePatch(dir string) (nativePatchManifest, []byte, error) {
	var p nativePatchManifest
	b, e := os.ReadFile(filepath.Join(dir, "patch", "patch-manifest.json"))
	if e != nil {
		return p, nil, e
	}
	if e = json.Unmarshal(b, &p); e != nil {
		return p, nil, e
	}
	if !verRE.MatchString(p.AppVersion) || !verRE.MatchString(p.ModVersion) || !shaRE.MatchString(p.ExpectedOriginalSHA) || !validTargetSHA(p) || !shaRE.MatchString(p.PatchDataSHA) {
		return p, nil, errors.New("invalid native patch manifest")
	}
	if p.AssetDataOffset < 0 || p.AssetCapacity <= 0 || p.AssetCapacity > 10<<20 || p.PatchDataLength <= 0 || p.PatchDataLength > p.AssetCapacity || len(p.DataLengthOffsets) == 0 || len(p.DataLengthOffsets) > 16 {
		return p, nil, errors.New("invalid patch layout")
	}
	b, e = os.ReadFile(filepath.Join(dir, "patch", "grim-poster-main.br"))
	if e != nil {
		return p, nil, e
	}
	if len(b) != p.PatchDataLength || hashBytes(b) != strings.ToLower(p.PatchDataSHA) {
		return p, nil, errors.New("patch payload checksum mismatch")
	}
	return p, b, nil
}
func hashBytes(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func containsHash(items []string, want string) bool {
	for _, h := range items {
		if strings.EqualFold(h, want) {
			return true
		}
	}
	return false
}
func patchedBytes(base, payload []byte, p nativePatchManifest) ([]byte, error) {
	if p.AssetDataOffset > len(base) || p.AssetCapacity > len(base)-p.AssetDataOffset {
		return nil, errors.New("patch exceeds official executable boundaries")
	}
	dst := bytes.Clone(base)
	clear(dst[p.AssetDataOffset : p.AssetDataOffset+p.AssetCapacity])
	copy(dst[p.AssetDataOffset:], payload)
	for _, off := range p.DataLengthOffsets {
		if off < 0 || off > len(dst)-8 {
			return nil, errors.New("invalid patch length position")
		}
		binary.LittleEndian.PutUint64(dst[off:off+8], uint64(len(payload)))
	}
	if p.ExpectedPatchedSHA != "" && !strings.EqualFold(hashBytes(dst), p.ExpectedPatchedSHA) {
		return nil, errors.New("generated executable did not match the expected SHA-256; no changes made")
	}
	return dst, nil
}
func replaceWithRollback(target string, data []byte, sha string) error {
	staged := target + ".grim-poster-new"
	switched := target + ".grim-poster-switch"
	if _, e := os.Stat(staged); e == nil {
		return fmt.Errorf("staging file already exists: %s", staged)
	}
	if _, e := os.Stat(switched); e == nil {
		return fmt.Errorf("recovery switch file already exists: %s", switched)
	}
	if e := os.WriteFile(staged, data, 0600); e != nil {
		return e
	}
	if h, e := hashFile(staged); e != nil || !strings.EqualFold(h, sha) {
		os.Remove(staged)
		return errors.New("staging hash mismatch")
	}
	if e := os.Rename(target, switched); e != nil {
		os.Remove(staged)
		return e
	}
	if e := os.Rename(staged, target); e != nil {
		os.Rename(switched, target)
		os.Remove(staged)
		return e
	}
	if h, e := hashFile(target); e != nil || !strings.EqualFold(h, sha) {
		os.Remove(target)
		if back := os.Rename(switched, target); back != nil {
			return fmt.Errorf("patch verification failed; manual recovery required from %s: %v", switched, back)
		}
		return errors.New("patched executable verification failed; original restored")
	}
	return os.Remove(switched)
}
func installPatchAt(path, dir, appVersion string, previousOfficialSHA string) error {
	p, payload, e := readNativePatch(dir)
	if e != nil {
		return e
	}
	if p.AppVersion != appVersion {
		return fmt.Errorf("patch targets BOTC %s, installed BOTC is %s", p.AppVersion, appVersion)
	}
	if p.ExpectedPatchedSHA == "" {
		target, err := deriveTargetSHA(path, payload, p)
		if err != nil {
			return err
		}
		p.ExpectedPatchedSHA = target
	}
	current, e := hashFile(path)
	if e != nil {
		return e
	}
	backup := path + ".grim-poster-original"
	backupSHA := ""
	if _, e := os.Stat(backup); e == nil {
		backupSHA, e = hashFile(backup)
		if e != nil {
			return e
		}
	}
	if strings.EqualFold(current, p.ExpectedPatchedSHA) {
		if !strings.EqualFold(backupSHA, p.ExpectedOriginalSHA) {
			return errors.New("patch is installed but a verified official recovery copy is missing")
		}
		return nil
	}
	original := strings.EqualFold(current, p.ExpectedOriginalSHA)
	validUpgrade := containsHash(p.SupportedUpgradeSHA, current) || (strings.EqualFold(backupSHA, p.ExpectedOriginalSHA) && !original)
	// Older patch hashes can be verified from the alternate payload layouts.
	if !original && !validUpgrade {
		for _, alt := range p.SupportedPatchPayloads {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(alt.File)))
			if err != nil || len(b) != alt.Length || !strings.EqualFold(hashBytes(b), alt.SHA256) {
				continue
			}
			if validEmbeddedPatch(path, b, p) {
				validUpgrade = true
				break
			}
		}
	}
	if !original && !validUpgrade {
		return errors.New("unrecognized BOTC executable; no file was modified")
	}
	if backupSHA != "" && !strings.EqualFold(backupSHA, p.ExpectedOriginalSHA) {
		if !original {
			return errors.New("existing recovery copy does not match the target BOTC version")
		}
		known := append([]string{"efc0abc595ac889d2cb33d11224313cfe83a97da4fc75b76cf2d25ac7c36e843"}, p.SupportedPreviousOfficialSHA...)
		if previousOfficialSHA != "" {
			known = append(known, previousOfficialSHA)
		}
		if !containsHash(known, backupSHA) {
			return errors.New("unknown recovery copy found; refusing to overwrite")
		}
		old := backup + "-previous-" + backupSHA[:16]
		if _, e := os.Stat(old); e == nil {
			return errors.New("archived backup already exists")
		}
		if e := os.Rename(backup, old); e != nil {
			return e
		}
		backupSHA = ""
	}
	if backupSHA == "" {
		if !original {
			return errors.New("an older patch exists but no verified original BOTC backup is available")
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if !strings.EqualFold(hashBytes(raw), p.ExpectedOriginalSHA) {
			return errors.New("original changed before backup")
		}
		if e := os.WriteFile(backup, raw, 0600); e != nil {
			return e
		}
		backupSHA, e = hashFile(backup)
		if e != nil || !strings.EqualFold(backupSHA, p.ExpectedOriginalSHA) {
			os.Remove(backup)
			return errors.New("failed to verify official recovery copy")
		}
	}
	raw, e := os.ReadFile(backup)
	if e != nil {
		return e
	}
	if !strings.EqualFold(hashBytes(raw), p.ExpectedOriginalSHA) {
		return errors.New("official backup checksum mismatch")
	}
	rebuilt, e := patchedBytes(raw, payload, p)
	if e != nil {
		return e
	}
	if e := replaceWithRollback(path, rebuilt, p.ExpectedPatchedSHA); e != nil {
		return e
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		folder := filepath.Join(local, "BOTC Grim Poster")
		if os.MkdirAll(folder, 0700) == nil {
			_ = os.WriteFile(filepath.Join(folder, "installed-version.txt"), []byte(p.ModVersion), 0600)
		}
	}
	return nil
}
func validEmbeddedPatch(path string, payload []byte, p nativePatchManifest) bool {
	f, e := os.Open(path)
	if e != nil {
		return false
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return false
	}
	if p.AssetDataOffset < 0 || p.AssetCapacity > int(info.Size())-p.AssetDataOffset {
		return false
	}
	region := make([]byte, p.AssetCapacity)
	if _, e = f.ReadAt(region, int64(p.AssetDataOffset)); e != nil {
		return false
	}
	if !bytes.Equal(region[:len(payload)], payload) {
		return false
	}
	for _, v := range region[len(payload):] {
		if v != 0 {
			return false
		}
	}
	for _, off := range p.DataLengthOffsets {
		if off < 0 || off > int(info.Size())-8 {
			return false
		}
		var v [8]byte
		if _, e = f.ReadAt(v[:], int64(off)); e != nil || binary.LittleEndian.Uint64(v[:]) != uint64(len(payload)) {
			return false
		}
	}
	return true
}

// Signed update ZIPs are verified by the pinned key before unpacking. The patch
// manifest and installed BOTC version must also match the signed metadata.
func fetchSignedPatch(desktop string, release signedRelease) (string, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := httpClient()
	client.Timeout = 3 * time.Minute
	raw, e := readBounded(ctx, client, signedUpdateURL+release.Asset, release.PackageBytes)
	if e != nil {
		return "", nil, e
	}
	if int64(len(raw)) != release.PackageBytes || !strings.EqualFold(hashBytes(raw), release.SHA256) {
		return "", nil, errors.New("signed package checksum or length mismatch")
	}
	tmp, e := os.MkdirTemp("", "grim-poster-signed-")
	if e != nil {
		return "", nil, e
	}
	cleanup := func() { os.RemoveAll(tmp) }
	zr, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		cleanup()
		return "", nil, e
	}
	var patchManifestPath string
	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > 50<<20 {
			cleanup()
			return "", nil, errors.New("oversized signed archive entry")
		}
		name := filepath.Clean(filepath.FromSlash(entry.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) || strings.Contains(name, ":") {
			cleanup()
			return "", nil, errors.New("unsafe signed archive path")
		}
		out := filepath.Join(tmp, name)
		if e = os.MkdirAll(filepath.Dir(out), 0700); e != nil {
			cleanup()
			return "", nil, e
		}
		in, ee := entry.Open()
		if ee != nil {
			cleanup()
			return "", nil, ee
		}
		data, ee := io.ReadAll(io.LimitReader(in, 50<<20+1))
		in.Close()
		if ee != nil || len(data) > 50<<20 {
			cleanup()
			return "", nil, errors.New("invalid archive content")
		}
		if e = os.WriteFile(out, data, 0600); e != nil {
			cleanup()
			return "", nil, e
		}
		if filepath.Base(name) == "patch-manifest.json" && filepath.Base(filepath.Dir(name)) == "patch" {
			if patchManifestPath != "" {
				cleanup()
				return "", nil, errors.New("ambiguous signed patch manifests")
			}
			patchManifestPath = out
		}
	}
	if patchManifestPath == "" {
		cleanup()
		return "", nil, errors.New("signed ZIP contains no patch manifest")
	}
	sourceDesktop := filepath.Dir(filepath.Dir(patchManifestPath))
	p, _, e := readNativePatch(sourceDesktop)
	if e != nil {
		cleanup()
		return "", nil, e
	}
	if p.AppVersion != release.AppVersion || p.ModVersion != release.ModVersion || !shaRE.MatchString(p.ExpectedPatchedSHA) {
		cleanup()
		return "", nil, errors.New("signed metadata differs from patch package or full target checksum is absent")
	}
	return sourceDesktop, cleanup, nil
}
func copyUpdatedDesktop(src, desktop string) error {
	// Only two versioned resources are consumed by the native launcher. Each
	// replacement is transactional on Windows, where os.Rename can't overwrite.
	for _, sub := range []string{"patch/patch-manifest.json", "patch/grim-poster-main.br"} {
		data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(sub)))
		if err != nil {
			return err
		}
		target := filepath.Join(desktop, filepath.FromSlash(sub))
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		temp, old := target+".incoming", target+".prior"
		if _, err = os.Stat(temp); err == nil {
			return fmt.Errorf("stale staged file: %s", temp)
		}
		if _, err = os.Stat(old); err == nil {
			return fmt.Errorf("stale backup file: %s", old)
		}
		if err = os.WriteFile(temp, data, 0600); err != nil {
			return err
		}
		existed := false
		if _, err = os.Stat(target); err == nil {
			existed = true
			if err = os.Rename(target, old); err != nil {
				os.Remove(temp)
				return err
			}
		}
		if err = os.Rename(temp, target); err != nil {
			os.Remove(temp)
			if existed {
				os.Rename(old, target)
			}
			return err
		}
		if existed {
			os.Remove(old)
		}
	}
	return nil
}
