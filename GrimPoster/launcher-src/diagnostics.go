package main

// Diagnostics stay on this device. The public app has no diagnostic upload path.
// The local detailed log is private to the current user's AppData.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const diagnosticsFile = "diagnostics.jsonl"

var diagMutex sync.Mutex

type diagnosticRecord struct {
	Time     string `json:"time"`
	Version  string `json:"version"`
	Event    string `json:"event"`
	Category string `json:"category"`
	Result   string `json:"result"`
}

func diagnosticDirectory() string {
	// If a damaged user profile prevents AppData writes, preserve the report
	// in TEMP instead of silently dropping the diagnostic entirely.
	if root := os.Getenv("LOCALAPPDATA"); root != "" {
		dir := filepath.Join(root, "Programs", "Grim Poster", "logs")
		if os.MkdirAll(dir, 0700) == nil {
			return dir
		}
	}
	return filepath.Join(os.TempDir(), "GrimPoster-Logs")
}
func classify(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, os.ErrPermission) || os.IsPermission(err) {
		return "permission_denied"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case 5:
			return "access_denied"
		case 32:
			return "file_in_use"
		case 2, 3:
			return "path_missing"
		}
	}
	t := strings.ToLower(err.Error())
	switch {
	case strings.Contains(t, "access is denied"), strings.Contains(t, "permission denied"):
		return "permission_denied"
	case strings.Contains(t, "file is being used"), strings.Contains(t, "sharing violation"):
		return "file_in_use"
	case strings.Contains(t, "timeout"), strings.Contains(t, "deadline exceeded"):
		return "network_timeout"
	case strings.Contains(t, "sha-256"), strings.Contains(t, "signature"):
		return "integrity_failure"
	case strings.Contains(t, "not found"), strings.Contains(t, "cannot find"):
		return "missing_file"
	default:
		return "other_error"
	}
}
func newRecord(event string, err error) diagnosticRecord {
	result := "ok"
	if err != nil {
		result = "error"
	}
	return diagnosticRecord{Time: time.Now().UTC().Format(time.RFC3339), Version: bootstrapVersion, Event: event, Category: classify(err), Result: result}
}
func appendRecord(path string, record diagnosticRecord) error {
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
func recordDiagnostic(event string, err error) {
	diagMutex.Lock()
	defer diagMutex.Unlock()
	dir := diagnosticDirectory()
	if os.MkdirAll(dir, 0700) != nil {
		return
	}
	_ = appendRecord(filepath.Join(dir, diagnosticsFile), newRecord(event, err))
	if err != nil { // Raw errors stay local. They are NEVER uploaded.
		rawPath := filepath.Join(dir, "details-local-only.log")
		f, e := os.OpenFile(rawPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e == nil {
			fmt.Fprintf(f, "%s [%s] %v\n", time.Now().UTC().Format(time.RFC3339), event, err)
			f.Close()
		}
	}
}
func recordPanic(message string, stack []byte) {
	recordDiagnostic("uncaught_panic", errors.New("uncaught panic"))
	diagMutex.Lock()
	defer diagMutex.Unlock()
	dir := diagnosticDirectory()
	if os.MkdirAll(dir, 0700) != nil {
		return
	}
	path := filepath.Join(dir, "panic-local-only.log")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e == nil {
		fmt.Fprintf(f, "%s PANIC %s\n%s\n", time.Now().UTC().Format(time.RFC3339), message, stack)
		f.Close()
	}
}
func lastRecord(path string) (diagnosticRecord, error) {
	f, e := os.Open(path)
	if e != nil {
		return diagnosticRecord{}, e
	}
	defer f.Close()
	// Avoid loading arbitrarily large logs into memory.
	stat, e := f.Stat()
	if e != nil {
		return diagnosticRecord{}, e
	}
	if stat.Size() > 1<<20 {
		_, e = f.Seek(stat.Size()-(1<<20), io.SeekStart)
		if e != nil {
			return diagnosticRecord{}, e
		}
	}
	raw, e := io.ReadAll(io.LimitReader(f, 1<<20))
	if e != nil {
		return diagnosticRecord{}, e
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) == 0 || len(bytes.TrimSpace(lines[len(lines)-1])) == 0 {
		return diagnosticRecord{}, errors.New("no diagnostics available")
	}
	var r diagnosticRecord
	e = json.Unmarshal(lines[len(lines)-1], &r)
	if e != nil {
		return r, e
	}
	return r, nil
}
func lastErrorRecord(path string) (diagnosticRecord, error) {
	f, e := os.Open(path)
	if e != nil {
		return diagnosticRecord{}, e
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil {
		return diagnosticRecord{}, e
	}
	if stat.Size() > 1<<20 {
		_, e = f.Seek(stat.Size()-(1<<20), io.SeekStart)
		if e != nil {
			return diagnosticRecord{}, e
		}
	}
	raw, e := io.ReadAll(io.LimitReader(f, 1<<20))
	if e != nil {
		return diagnosticRecord{}, e
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var record diagnosticRecord
		if json.Unmarshal(lines[i], &record) != nil || record.Result != "error" {
			continue
		}
		parsed, e := time.Parse(time.RFC3339, record.Time)
		if e != nil || time.Since(parsed) > 7*24*time.Hour {
			continue
		}
		return record, nil
	}
	return diagnosticRecord{}, errors.New("no recent errors or crashes (last seven days)")
}
