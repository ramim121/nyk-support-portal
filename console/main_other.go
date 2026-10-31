//go:build !windows

// On non-Windows platforms the desktop console isn't built as a native window;
// this stub opens the console URL in the default browser instead, so the
// package still builds and runs everywhere (the hosted browser console is the
// cross-platform path).
package main

import (
	"flag"
	"fmt"
	"os/exec"
	"runtime"
)

var ServerURL = "https://support.example.com/console"

func main() {
	url := flag.String("url", ServerURL, "console web app URL to open")
	flag.Parse()

	fmt.Printf("NYK Support Portal console: %s\n", *url)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", *url)
	default:
		cmd = exec.Command("xdg-open", *url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("Open this URL in your browser: %s\n", *url)
	}
}
