package dynamo

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// awsFolder is where the AWS CLI keeps your profiles, as messages name it.
const awsFolder = `%USERPROFILE%\.aws`

// cliInstallHelp says how to get the AWS CLI on Windows. SpyDB doesn't
// install its own here: AWS's Windows installer is for all users, so it
// asks for an administrator's permission, and Windows shows that itself.
const cliInstallHelp = "in PowerShell: winget install -e --id Amazon.AWSCLI, or AWS's installer: https://awscli.amazonaws.com/AWSCLIV2.msi"

// awsCLIPlaces is where AWS's installer puts the AWS CLI. PATH finds it
// too, but SpyDB's PATH is the one it started with: a CLI installed while
// SpyDB is open is only found here, by name.
func awsCLIPlaces() []string {
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	return []string{filepath.Join(programFiles, "Amazon", "AWSCLIV2", "aws.exe")}
}

// createNoWindow is CREATE_NO_WINDOW: the program gets no console window.
const createNoWindow = 0x08000000

// hideConsole keeps a console program (aws.exe) from flashing a black
// window each time SpyDB runs it: Windows gives every console program
// started from a windowed app a console of its own unless told not to.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
