//go:build !windows

package main

import "errors"

func launchManager(root, desktop string, first bool) error {
	return errors.New("the native Grim Poster interface requires Windows")
}

func launchManagerWithFallback(root, desktop string, first bool) error {
	return errors.New("adaptive Grim Poster manager requires Windows")
}
