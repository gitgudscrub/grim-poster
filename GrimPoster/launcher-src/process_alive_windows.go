//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func processStillRunning(pid int) bool {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	handle, _, openErr := kernel.NewProc("OpenProcess").Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if handle == 0 {
		// Non-elevated launchers cannot always query elevated processes.
		// Permission denied is not proof that the process has exited.
		return openErr == syscall.Errno(5)
	}
	defer kernel.NewProc("CloseHandle").Call(handle)
	var code uint32
	ok, _, _ := kernel.NewProc("GetExitCodeProcess").Call(handle, uintptr(unsafe.Pointer(&code)))
	return ok != 0 && code == 259 // STILL_ACTIVE
}
