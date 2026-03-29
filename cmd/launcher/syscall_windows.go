//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hiddenWindow returns SysProcAttr that hides the console window on Windows.
func hiddenWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// ensure exec is imported (used in main.go)
var _ = exec.Command
