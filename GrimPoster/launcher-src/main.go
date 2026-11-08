// Grim Poster bootstrap for Windows x64. Uses only the Go standard library.
// It installs the editable, signed-update-aware companion into LocalAppData.
package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

//go:embed bundled.zip
var bundled []byte

const bootstrapVersion = "0.2.0"

// runWithCrashHandler keeps the recorder active across bootstrap, the GUI,
// and the companion. Go panics are recoverable; OS-level termination is detected
// by the session marker on the next launch.
func main() { os.Exit(runWithCrashHandler()) }

func runWithCrashHandler() (exitCode int) {
	recoverAbandonedSessions()
	session := beginCrashSession(processRole(os.Args))
	defer func() {
		if r := recover(); r != nil {
			recordPanic(fmt.Sprint(r), debug.Stack())
			updateCrashStage("uncaught_panic")
			displayError("Grim Poster stopped unexpectedly.\n\nCrash details were saved locally.\nOpen View logs on your next launch to review them.")
			exitCode = 2
		}
		// Non-panic errors have already been logged, so mark the session as
		// completed. A hard termination leaves its marker for recovery.
		endCrashSession(session)
	}()
	recordDiagnostic("launcher_start", nil)
	updateCrashStage("bootstrap_start")
	if err := run(); err != nil {
		recordDiagnostic("launcher_start_failed", err)
		updateCrashStage("startup_error")
		displayError("Grim Poster could not start:\n\n" + err.Error() + "\n\nCrash details saved locally under Grim Poster/logs.")
		return 1
	}
	updateCrashStage("normal_exit")
	return 0
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "--companion" {
		updateCrashStage("companion_starting")
		return runNativeCompanion()
	}
	updateCrashStage("locating_installation")
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return errors.New("LOCALAPPDATA is missing; sign in to Windows normally")
	}
	appRoot := filepath.Join(local, "Programs", "Grim Poster")
	desktop := filepath.Join(appRoot, "desktop")
	installedVersionPath := filepath.Join(appRoot, "bootstrap-version.txt")
	first := false
	if _, err := os.Stat(installedVersionPath); errors.Is(err, os.ErrNotExist) {
		first = true
	}
	// Do not overwrite a signed-update-installed version with an older embedded patch.
	// New launcher versions can ship more current payloads; otherwise preserve newer contents.
	marker, _ := os.ReadFile(installedVersionPath)
	if strings.TrimSpace(string(marker)) != bootstrapVersion {
		updateCrashStage("extracting_support_files")
		// Preserve newer signed patch files but add the new GUI if this is first use.
		preserveExisting := false // Runtime releases always install their own metadata.
		if err := unzipBundle(bundled, appRoot, preserveExisting); err != nil {
			return err
		}
		if err := os.MkdirAll(appRoot, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(installedVersionPath, []byte(bootstrapVersion+"\n"), 0600); err != nil {
			return err
		}
	}
	// Copy the launcher itself into the user's app directory for a stable shortcut target.
	destination := filepath.Join(appRoot, "GrimPoster.exe")
	own, err := os.Executable()
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(own), filepath.Clean(destination)) {
		if err := copyIfNew(own, destination); err != nil {
			return err
		}
	}
	// --manager opens diagnostics and controls even when auto-launch is ready.
	forceManager := len(os.Args) > 1 && os.Args[1] == "--manager"
	// Keep the native manager in a separately monitored child. If its native GUI
	// fails, the parent can offer the known-good Windows Forms manager where policy allows.
	if len(os.Args) > 1 && os.Args[1] == "--native-manager" {
		updateCrashStage("native_manager_starting")
		force := false
		for _, arg := range os.Args[2:] {
			if arg == "--force-manager" {
				force = true
			}
			if arg == "--recovered-crash" {
				wasPreviousCrash = true
				force = true
			}
		}
		return launchManager(appRoot, desktop, first || force)
	}
	updateCrashStage("supervising_manager")
	return launchManagerWithFallback(appRoot, desktop, first || forceManager || recoveredPreviousCrash())
}

func newerInstalledPatch(desktop string) bool {
	b, err := os.ReadFile(filepath.Join(desktop, "patch", "patch-manifest.json"))
	if err != nil {
		return false
	}
	var info struct {
		ModVersion string `json:"modVersion"`
	}
	if json.Unmarshal(b, &info) != nil {
		return false
	}
	parts := strings.Split(info.ModVersion, ".")
	if len(parts) != 3 {
		return true
	} // Unknown installed version: fail closed; avoid overwriting.
	var nums [3]int
	for i, s := range parts {
		n, e := strconv.Atoi(s)
		if e != nil {
			return true
		}
		nums[i] = n
	}
	// The bootstrap carries the 0.1.67 patch. Preserve newer patches.
	if nums[0] > 0 || nums[1] > 1 || (nums[0] == 0 && nums[1] == 1 && nums[2] > 67) {
		return true
	}
	return false
}
func unzipBundle(data []byte, dest string, onlyMissing bool) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > 50*1024*1024 {
			return fmt.Errorf("unexpectedly large package entry: %s", f.Name)
		}
		cleaned := filepath.Clean(filepath.FromSlash(f.Name))
		if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) || !strings.HasPrefix(cleaned, "desktop"+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe bundled path: %s", f.Name)
		}
		target := filepath.Join(dest, cleaned)
		if onlyMissing && cleaned != filepath.Join("desktop", "Start-GrimPosterManager.ps1") &&
			cleaned != filepath.Join("desktop", "Check-GrimPosterUpdate.ps1") {
			if _, err := os.Stat(target); err == nil {
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 50*1024*1024+1))
		rc.Close()
		if err != nil {
			return err
		}
		if int64(len(b)) > 50*1024*1024 {
			return errors.New("bundled file too large")
		}
		if err := os.WriteFile(target, b, 0600); err != nil {
			return err
		}
	}
	return nil
}
func copyIfNew(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(dst); err == nil && bytes.Equal(existing, b) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	// Avoid corrupting an existing working launcher if a copy fails midway.
	temp := dst + ".new"
	if err := os.WriteFile(temp, b, 0700); err != nil {
		return err
	}
	// Windows cannot rename onto an existing target. Preserve a rollback copy.
	backup := dst + ".previous"
	if _, err := os.Stat(dst); err == nil {
		os.Remove(backup)
		if err := os.Rename(dst, backup); err != nil {
			os.Remove(temp)
			return err
		}
	}
	if err := os.Rename(temp, dst); err != nil {
		os.Remove(temp)
		if _, oldErr := os.Stat(backup); oldErr == nil {
			os.Rename(backup, dst)
		}
		return err
	}
	// Retain one prior launcher for manual recovery during stability testing.
	return nil
}
