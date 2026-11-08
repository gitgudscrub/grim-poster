//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func hiddenWindow() *syscall.SysProcAttr { return &syscall.SysProcAttr{HideWindow: true} }
func displayError(message string) {
	dll := syscall.NewLazyDLL("user32.dll")
	call := dll.NewProc("MessageBoxW")
	body, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("Grim Poster")
	call.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), 0x10)
}
