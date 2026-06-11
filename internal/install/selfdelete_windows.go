//go:build windows

package install

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows locks a running executable's image file, so it cannot delete itself
// directly. scheduleSelfDelete hands the job to a detached, hidden cmd.exe
// that retries `del` for up to ~30s; the first attempt after this process
// exits succeeds. The child outlives its parent, so the deletion lands even
// though we are gone. Once the binary is gone it also removes the now-empty
// install folder (`rd` without /S deletes only an empty directory, so a dir
// that still holds anything is left untouched).
func scheduleSelfDelete(path string) error {
	dir := filepath.Dir(path)
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
		// cmd /C strips the outer quotes and keeps the inner ones, so paths
		// with spaces survive. ping is the sleep: timeout(1) needs a console.
		CmdLine: fmt.Sprintf(
			`cmd.exe /C "for /L %%i in (1,1,30) do (del /F /Q "%s" >NUL 2>&1 & if not exist "%s" (rd "%s" >NUL 2>&1 & exit /B) & ping -n 2 127.0.0.1 >NUL)"`,
			path, path, dir),
	}
	return cmd.Start()
}
