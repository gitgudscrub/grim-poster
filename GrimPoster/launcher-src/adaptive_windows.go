//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// We never bypass or modify a system PowerShell execution policy. This is
// a compatibility method only where local unsigned scripts are permitted.
func legacyAvailable(desktop string) bool {
	return false
}

func historicalLegacyAvailable(desktop string) bool {
	script := filepath.Join(desktop, "Start-GrimPosterCompanion.ps1")
	if _, err := os.Stat(script); err != nil {
		return false
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Get-ExecutionPolicy")
	cmd.SysProcAttr = hiddenWindow()
	out, e := cmd.Output()
	if e != nil {
		return false
	}
	effective := strings.TrimSpace(string(out))
	return effective == "RemoteSigned" || effective == "Unrestricted" || effective == "Bypass"
}
func startLegacyCompanion(root, desktop string) error {
	if !legacyAvailable(desktop) {
		return errors.New("legacy mode unavailable: current PowerShell policy does not permit unsigned local scripts")
	}
	script := filepath.Join(desktop, "Start-GrimPosterCompanion.ps1")
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(logDir, "compatibility-launch-"+time.Now().Format("20060102-150405")+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	// Do not use -ExecutionPolicy Bypass and do not elevate.
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-File", script)
	cmd.SysProcAttr = hiddenWindow()
	cmd.Stdout = f
	cmd.Stderr = f
	if err = cmd.Start(); err != nil {
		f.Close()
		return err
	}
	done := make(chan error, 1)
	go func() {
		e := cmd.Wait()
		if e != nil {
			fmt.Fprintf(f, "Legacy companion exited: %v\n", e)
			recordDiagnostic("legacy_companion_failed", e)
		}
		f.Close()
		done <- e
	}()
	select {
	case e := <-done:
		if e != nil {
			return fmt.Errorf("legacy companion exited immediately: %w", e)
		}
		return errors.New("legacy companion closed immediately")
	case <-time.After(1100 * time.Millisecond):
		return nil
	}
}

// The adaptive mode only retries the OTHER companion if the first process
// fails immediately and BOTC is not running; it never interrupts an active game.
func startAdaptiveCompanion(root, desktop, gameVersion string) (string, error) {
	settings := loadAdaptive(root)
	settings.Preference = modeNative
	allowed := legacyAvailable(desktop)
	mode, why := chosenMode(settings, gameVersion, allowed)
	if mode == "" {
		return "", errors.New(why)
	}
	recordDiagnostic("capture_mode_"+mode, nil)
	var err error
	if mode == modeNative {
		err = startNativeCompanion(root)
	} else {
		err = startLegacyCompanion(root, desktop)
	}
	if err == nil {
		return mode, nil
	}
	recordDiagnostic("capture_mode_start_failed", err)
	if settings.Preference == modeAdaptive && !processExists("botc-app.exe") {
		if mode == modeNative && allowed {
			if legacyErr := startLegacyCompanion(root, desktop); legacyErr == nil {
				recordDiagnostic("fallback_to_legacy", nil)
				return modeLegacy, nil
			} else {
				return "", fmt.Errorf("native start: %w; legacy start: %v", err, legacyErr)
			}
		}
		if mode == modeLegacy {
			if nativeErr := startNativeCompanion(root); nativeErr == nil {
				recordDiagnostic("fallback_to_native", nil)
				return modeNative, nil
			} else {
				return "", fmt.Errorf("legacy start: %w; native start: %v", err, nativeErr)
			}
		}
	}
	return "", err
}

// The bootstrap remains alive while the native manager is running. A crash or
// nonzero exit triggers a legacy GUI if its unsigned local scripts are allowed.
// Closing the native manager normally does not trigger a fallback.
func launchManagerWithFallback(root, desktop string, force bool) error {
	own, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"--native-manager"}
	if force {
		args = append(args, "--force-manager")
	}
	if recoveredPreviousCrash() {
		args = append(args, "--recovered-crash")
	}
	cmd := exec.Command(own, args...)
	// This child owns the visible Win32 manager. Do NOT hide its window:
	// hiding it makes the launcher appear to exit without showing anything.
	// Only headless helpers (PowerShell/companion processes) use hiddenWindow().
	// Catch runtime failures that terminate the native GUI before its own
	// recorder can flush (including panics outside the main goroutine).
	logDir := filepath.Join(root, "logs")
	if e := os.MkdirAll(logDir, 0700); e != nil {
		return e
	}
	childLog, e := os.OpenFile(filepath.Join(logDir, "native-manager-"+time.Now().Format("20060102-150405")+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	defer childLog.Close()
	cmd.Stdout, cmd.Stderr = childLog, childLog
	if err = cmd.Start(); err != nil {
		return startLegacyManagerOrError(root, desktop, fmt.Errorf("native manager did not start: %w", err), force)
	}
	if err = cmd.Wait(); err != nil {
		fmt.Fprintf(childLog, "Native manager exited abnormally: %v\n", err)
		_ = childLog.Sync()
		updateCrashStage("native_manager_child_failed")
		recordDiagnostic("native_manager_failed", err)
		return startLegacyManagerOrError(root, desktop, fmt.Errorf("native manager stopped: %w", err), force)
	}
	return nil
}
func startLegacyManagerOrError(root, desktop string, cause error, force bool) error {
	if !legacyAvailable(desktop) {
		return fmt.Errorf("%w. Legacy startup unavailable under current PowerShell policy. See local diagnostics", cause)
	}
	script := filepath.Join(desktop, "Start-GrimPosterManager.ps1")
	args := []string{"-NoProfile", "-STA", "-File", script}
	if force {
		args = append(args, "-FirstLaunch")
	}
	cmd := exec.Command("powershell.exe", args...)
	cmd.SysProcAttr = hiddenWindow()
	logDir := filepath.Join(root, "logs")
	os.MkdirAll(logDir, 0700)
	file, e := os.OpenFile(filepath.Join(logDir, "legacy-manager-startup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer file.Close()
	cmd.Stdout = file
	cmd.Stderr = file
	recordDiagnostic("fallback_to_legacy_manager", nil)
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("native startup: %v; legacy GUI: %w", cause, e)
	}
	return nil
}
