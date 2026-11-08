//go:build !windows

package main

import "os"

func processStillRunning(pid int) bool { return pid == os.Getpid() }
