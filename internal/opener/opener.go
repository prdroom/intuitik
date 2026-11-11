// Package opener starts the browser for the login address (packet M1-T2 S18). It is
// the only product package that imports os/exec (packet M1-T4 R4), so no other part of
// intuitik can start a program such as kubectl.
package opener

import (
	"os/exec"
	"runtime"
)

// Open passes url to the browser as one argument. With browser set (the BROWSER
// environment variable), its whole value is one executable started without a shell;
// otherwise the OS default opener. Run waits for the executable: one that starts and
// then exits unsuccessfully is an open failure too.
func Open(browser, url string) error {
	if browser != "" {
		return exec.Command(browser, url).Run()
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Run()
}
