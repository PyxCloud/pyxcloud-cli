package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"github.com/pyxcloud/pyxcloud-cli/cmd"
)

func main() {
	// Custom Protocol Handler Interceptor
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "pyxcloud://") {
		uri := strings.TrimPrefix(os.Args[1], "pyxcloud://")
		// Parse uri (e.g. "proxy" or "proxy/")
		uri = strings.TrimSuffix(uri, "/")
		
		// Map it to the internal command
		var internalArgs []string
		if uri == "proxy" {
			internalArgs = []string{"proxy"}
		} else {
			// fallback/other commands
			internalArgs = strings.Split(uri, "/")
		}

		// Re-spawn self in background to hide the DOS window (Windows) or detach (Linux/Mac)
		executable, err := os.Executable()
		if err == nil {
			bgCmd := exec.Command(executable, internalArgs...)
			
			// OS specific stealth
			if runtime.GOOS == "windows" {
				bgCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			}
			
			_ = bgCmd.Start()
			// Exit immediately so the browser launcher feels instantaneous and no console window stays open
			os.Exit(0)
		}
	}

	cmd.Execute()
}
