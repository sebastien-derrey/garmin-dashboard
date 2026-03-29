//go:build windows

package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	exeDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	serverExe := filepath.Join(exeDir, "garmin_dashboard.exe")

	// Start the server if it isn't already answering
	if !serverReady() {
		cmd := exec.Command(serverExe)
		cmd.Dir = exeDir
		// No console window
		cmd.SysProcAttr = hiddenWindow()
		cmd.Start() //nolint:errcheck — best-effort launch

		// Wait up to 10 s for the server to become ready
		for i := 0; i < 40; i++ {
			time.Sleep(250 * time.Millisecond)
			if serverReady() {
				break
			}
		}
	}

	openAppWindow("http://localhost:8080")
}

func serverReady() bool {
	resp, err := http.Get("http://localhost:8080/api/status")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func openAppWindow(url string) {
	// Try Chrome locations in order of likelihood
	candidates := []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\Edge\Application\msedge.exe`),
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			exec.Command(p, "--app="+url, "--window-size=1440,900").Start() //nolint:errcheck
			return
		}
	}

	// Fallback: open in whatever the default browser is
	exec.Command("cmd", "/c", "start", url).Start() //nolint:errcheck
}
