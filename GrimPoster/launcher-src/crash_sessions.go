package main

// Crash sessions persist a small, path-free status record. They are recovered
// on the next launch even if Windows kills the process without a Go panic.
// No crash data is uploaded without the Storyteller's explicit permission.

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

type crashMarker struct {
	Version string `json:"version"`
	Role    string `json:"role"`
	PID     int    `json:"pid"`
	Started string `json:"started"`
	Updated string `json:"updated"`
	Stage   string `json:"stage"`
}

type crashSession struct {
	mu       sync.Mutex
	marker   crashMarker
	path     string
	stop     chan struct{}
	finished chan struct{}
	closed   bool
}

var currentCrashSession *crashSession

func processRole(args []string) string {
	if len(args) > 1 {
		switch args[1] {
		case "--companion":
			return "companion"
		case "--native-manager":
			return "native_manager"
		}
	}
	return "launcher"
}
func sessionDir() string { return filepath.Join(diagnosticDirectory(), "sessions") }

func writeMarker(p string, m crashMarker) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
func beginCrashSession(role string) *crashSession {
	now := time.Now().UTC().Format(time.RFC3339)
	s := &crashSession{
		marker: crashMarker{Version: bootstrapVersion, Role: role, PID: os.Getpid(), Started: now, Updated: now, Stage: "process_started"},
		path:   filepath.Join(sessionDir(), fmt.Sprintf("session-%d.json", os.Getpid())),
		stop:   make(chan struct{}), finished: make(chan struct{}),
	}
	_ = writeMarker(s.path, s.marker)
	currentCrashSession = s
	go func() {
		defer close(s.finished)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.update("")
			}
		}
	}()
	return s
}
func (s *crashSession) update(stage string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if stage != "" {
		s.marker.Stage = stage
	}
	s.marker.Updated = time.Now().UTC().Format(time.RFC3339)
	_ = writeMarker(s.path, s.marker)
}
func updateCrashStage(stage string) {
	if currentCrashSession != nil {
		currentCrashSession.update(stage)
	}
}
func endCrashSession(s *crashSession) {
	if s == nil {
		return
	}
	close(s.stop)
	<-s.finished
	s.mu.Lock()
	s.closed = true
	_ = os.Remove(s.path)
	s.mu.Unlock()
}

var wasPreviousCrash bool

func recoveredPreviousCrash() bool { return wasPreviousCrash }

func recoverAbandonedSessions() {
	root := sessionDir()
	files, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasPrefix(f.Name(), "session-") || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, f.Name())
		b, err := os.ReadFile(path)
		if err != nil || len(b) > 4096 {
			continue
		}
		var m crashMarker
		if json.Unmarshal(b, &m) != nil || m.PID < 1 {
			continue
		}
		if processStillRunning(m.PID) {
			continue
		}
		started, err := time.Parse(time.RFC3339, m.Started)
		if err != nil {
			continue
		}
		if time.Since(started) < 2*time.Second {
			continue
		}
		_ = os.Remove(path) // avoid repeated reports for the same incident
		if time.Since(started) > 14*24*time.Hour {
			continue
		}
		wasPreviousCrash = true
		recordDiagnostic("previous_session_unexpected_exit", errors.New("process ended without completing its session"))
		// Human-readable, privacy-minimised recovery summary; no raw paths.
		summary := fmt.Sprintf("Grim Poster detected a previous unexpected termination.\nVersion: %s\nComponent: %s\nStarted (UTC): %s\nLast observed (UTC): %s\nLast stage: %s\n\nThis could indicate a crash, a forced termination or a system shutdown.\nNo screenshots, tokens or Discord messages were collected.\n", m.Version, m.Role, m.Started, m.Updated, m.Stage)
		_ = os.WriteFile(filepath.Join(diagnosticDirectory(), "last-unexpected-exit.txt"), []byte(summary), 0600)
	}
}
