//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

func hiddenWindow() *syscall.SysProcAttr { return nil }
func displayError(message string)        { fmt.Fprintln(os.Stderr, message) }

func runNativeCompanion() error { return fmt.Errorf("Windows only") }
