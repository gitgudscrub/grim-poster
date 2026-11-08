//go:build windows

package main

import (
	"os"
	"strings"
	"testing"
)

func TestNativeManagerPinsWindowThread(t *testing.T) {
	b, err := os.ReadFile("ui_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	start := strings.Index(s, "func launchManager(")
	end := strings.Index(s, "func (ui *nativeUI) refresh()")
	if start == -1 || end <= start {
		t.Fatal("cannot locate native manager")
	}
	manager := s[start:end]
	if !strings.Contains(manager, "runtime.LockOSThread()") || !strings.Contains(manager, "defer runtime.UnlockOSThread()") {
		t.Fatal("the Win32 native manager must run on a pinned OS thread")
	}
	if !strings.Contains(manager, "procGetMessageW.Call") {
		t.Fatal("missing Win32 event loop")
	}
}
