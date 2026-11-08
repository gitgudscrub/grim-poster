package main

// Adaptive compatibility state intentionally stores only a small non-sensitive
// health summary. Native failures are used to choose the legacy companion on
// *the next launch*, never to restart an active BOTC game.
import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	modeAdaptive = "adaptive"
	modeNative   = "native"
	modeLegacy   = "legacy"
)

type adaptiveSettings struct {
	Preference    string `json:"preference"`
	NativeTrouble bool   `json:"nativeTrouble"`
	LastOutcome   string `json:"lastOutcome,omitempty"`
	AppVersion    string `json:"appVersion,omitempty"`
	LastChange    string `json:"lastChange,omitempty"`
}

var adaptiveMutex sync.Mutex

func adaptivePath(root string) string { return filepath.Join(root, "adaptive-settings.json") }
func loadAdaptive(root string) adaptiveSettings {
	s := adaptiveSettings{Preference: modeAdaptive}
	b, e := os.ReadFile(adaptivePath(root))
	if e != nil {
		return s
	}
	if len(b) > 4096 || json.Unmarshal(b, &s) != nil {
		return adaptiveSettings{Preference: modeAdaptive}
	}
	if !validMode(s.Preference) {
		s.Preference = modeAdaptive
	}
	return s
}
func validMode(s string) bool { return s == modeAdaptive || s == modeNative || s == modeLegacy }
func nextMode(s string) string {
	switch s {
	case modeAdaptive:
		return modeNative
	case modeNative:
		return modeLegacy
	default:
		return modeAdaptive
	}
}
func saveAdaptive(root string, s adaptiveSettings) error {
	if !validMode(s.Preference) {
		return errors.New("invalid compatibility preference")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return e
	}
	dest := adaptivePath(root)
	tmp := dest + ".new"
	if e = os.WriteFile(tmp, append(b, '\n'), 0600); e != nil {
		return e
	}
	// Windows does not permit rename over an existing destination.
	if e = os.Remove(dest); e != nil && !errors.Is(e, os.ErrNotExist) {
		os.Remove(tmp)
		return e
	}
	if e = os.Rename(tmp, dest); e != nil {
		os.Remove(tmp)
	}
	return e
}
func setPreference(root, mode string) error {
	adaptiveMutex.Lock()
	defer adaptiveMutex.Unlock()
	s := loadAdaptive(root)
	s.Preference = mode
	return saveAdaptive(root, s)
}
func setNativeHealth(root, appVersion string, healthy bool, outcome string) {
	adaptiveMutex.Lock()
	defer adaptiveMutex.Unlock()
	s := loadAdaptive(root)
	// Ignore old health after a game update (new WebView2 might behave differently).
	s.AppVersion = appVersion
	s.NativeTrouble = !healthy
	s.LastOutcome = outcome
	s.LastChange = time.Now().UTC().Format(time.RFC3339)
	_ = saveAdaptive(root, s)
	if healthy {
		recordDiagnostic("native_capture_ready", nil)
	} else {
		recordDiagnostic("native_capture_unavailable", fmt.Errorf("WebView2 capture unavailable"))
	}
}
func chosenMode(s adaptiveSettings, gameVersion string, legacyAllowed bool) (string, string) {
	switch s.Preference {
	case modeNative:
		return modeNative, "Manually selected"
	case modeLegacy:
		if legacyAllowed {
			return modeLegacy, "Manually selected"
		}
		return "", "Legacy requires PowerShell script permission"
	}
	if s.NativeTrouble && s.AppVersion == gameVersion && legacyAllowed {
		return modeLegacy, "Previous native capture failed; trying compatibility"
	}
	return modeNative, "Native capture preferred"
}
func adaptiveDescription(s adaptiveSettings, gameVersion string, legacyAllowed bool) string {
	m, reason := chosenMode(s, gameVersion, legacyAllowed)
	if m == "" {
		return "Mode: Legacy blocked by policy"
	}
	title := strings.ToUpper(m[:1]) + m[1:]
	if s.Preference == modeAdaptive {
		title = "Adaptive > " + title
	}
	return title + "  |  " + reason
}

// Do not silently enter a known-broken capture mode when an automatic fallback
// is prohibited by the user's script policy. Expose the manager instead.
func safeToAutoLaunch(s adaptiveSettings, gameVersion string, legacyAllowed bool) bool {
	mode, _ := chosenMode(s, gameVersion, legacyAllowed)
	if mode == "" {
		return false
	}
	return !(s.Preference == modeAdaptive && s.NativeTrouble && s.AppVersion == gameVersion && !legacyAllowed)
}
