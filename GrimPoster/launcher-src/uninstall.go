package main

// Fail-closed Grim Poster uninstaller. BOTC itself is NEVER removed. It is
// restored only when both the active patch and original backup are verified.
// Nothing from the user's personal BOTC configuration or game data is touched.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type uninstallResult struct {
	Restored        bool
	AlreadyOfficial bool
	GameNotFound    bool
}

// planUninstallPatch reads a manifest without depending on PowerShell or even
// the compressed JS payload. Signed update installations may update their
// patch manifest, and the original executable backup remains authoritative.
func readUninstallManifest(desktop string) (nativePatchManifest, error) {
	var p nativePatchManifest
	b, err := os.ReadFile(filepath.Join(desktop, "patch", "patch-manifest.json"))
	if err != nil {
		return p, fmt.Errorf("cannot verify Grim Poster patch manifest: %w", err)
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	if !shaRE.MatchString(p.ExpectedOriginalSHA) || !validTargetSHA(p) || !verRE.MatchString(p.AppVersion) {
		return p, errors.New("invalid recovery manifest; game left unchanged")
	}
	return p, nil
}

// restoreBotcPatch only modifies an explicitly recognized BOTC patch backed by
// an exact, verified official executable. This is deliberately stricter than
// the installer, which supports more complex upgrade paths.
func restoreBotcPatch(exe, desktop, detectedVersion string) (uninstallResult, error) {
	result := uninstallResult{}
	if exe == "" {
		result.GameNotFound = true
		return result, nil
	}
	if !strings.EqualFold(filepath.Base(exe), "botc-app.exe") {
		return result, errors.New("unrecognized BOTC executable name; nothing changed")
	}
	if _, err := os.Stat(exe); err != nil {
		return result, fmt.Errorf("cannot inspect BOTC executable: %w", err)
	}
	p, err := readUninstallManifest(desktop)
	if err != nil {
		return result, err
	}
	if detectedVersion != p.AppVersion {
		return result, fmt.Errorf("BOTC %s does not match Grim Poster recovery manifest for %s; nothing changed", detectedVersion, p.AppVersion)
	}
	actual, err := hashFile(exe)
	if err != nil {
		return result, err
	}
	backup := exe + ".grim-poster-original"
	backupSHA := ""
	if _, err := os.Stat(backup); err == nil {
		backupSHA, err = hashFile(backup)
		if err != nil {
			return result, err
		}
		if !strings.EqualFold(backupSHA, p.ExpectedOriginalSHA) {
			return result, errors.New("the original BOTC recovery copy has an unexpected checksum; nothing changed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("cannot inspect recovery copy: %w", err)
	}
	if p.ExpectedPatchedSHA == "" && p.PatchVerification == "derive-from-verified-original" && backupSHA != "" {
		verified, payload, err := readNativePatch(desktop)
		if err != nil {
			return result, err
		}
		derived, err := deriveTargetSHA(exe, payload, verified)
		if err != nil {
			return result, err
		}
		p.ExpectedPatchedSHA = derived
	}
	if strings.EqualFold(actual, p.ExpectedOriginalSHA) {
		// If the game is already official, do not rewrite the official EXE.
		// Removing a verified, now-redundant backup is safe.
		if backupSHA != "" {
			if err := os.Remove(backup); err != nil {
				return result, fmt.Errorf("official game intact, but old recovery copy could not be removed: %w", err)
			}
		}
		result.AlreadyOfficial = true
		return result, nil
	}
	if !strings.EqualFold(actual, p.ExpectedPatchedSHA) && !containsHash(p.SupportedUpgradeSHA, actual) {
		return result, errors.New("BOTC executable does not match a verified Grim Poster patch; no game files or support files were removed")
	}
	if backupSHA == "" {
		return result, errors.New("original BOTC backup is missing; uninstall stopped without changing the game")
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		return result, err
	}
	if !strings.EqualFold(hashBytes(data), p.ExpectedOriginalSHA) {
		return result, errors.New("official BOTC backup changed during verification; nothing changed")
	}
	if err := replaceWithRollback(exe, data, p.ExpectedOriginalSHA); err != nil {
		return result, err
	}
	result.Restored = true
	// Removal is after verified restoration. A failed deletion doesn't negate
	// the restored game, but cleanup is suspended until the user resolves it.
	if err := os.Remove(backup); err != nil {
		return result, fmt.Errorf("BOTC restored, but backup cleanup failed: %w", err)
	}
	return result, nil
}

// removeManagedSupport removes only application-owned files. User screenshots,
// game saves, diagnostics and any unknown files remain. A launcher that is
// currently executing may be locked by Windows; that file can be deleted by
// the user after the manager closes.
func removeManagedSupport(root string) ([]string, error) {
	var kept []string
	desktop := filepath.Join(root, "desktop")
	if err := os.RemoveAll(desktop); err != nil {
		return kept, fmt.Errorf("failed to remove Grim Poster support scripts: %w", err)
	}
	for _, name := range []string{"bootstrap-version.txt", "adaptive-settings.json", "botc-path.txt", "GrimPoster.exe", "GrimPoster.exe.previous", "GrimPoster.exe.new"} {
		file := filepath.Join(root, name)
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			// The running Windows launcher may be in this folder. Never make
			// a successfully restored BOTC install fail merely for that.
			if name == "GrimPoster.exe" {
				kept = append(kept, file)
				continue
			}
			return kept, fmt.Errorf("could not remove %s: %w", name, err)
		}
	}
	// This file holds only Grim Poster's recorded version, not BOTC state.
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		f := filepath.Join(local, "BOTC Grim Poster", "installed-version.txt")
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return kept, fmt.Errorf("could not remove old Grim Poster version marker: %w", err)
		}
	}
	return kept, nil
}
