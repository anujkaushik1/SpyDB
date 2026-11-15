package dynamo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Windows' own fake AWS CLI: aws.cmd, a batch file (the shell-script fakes
// skip here). It answers --version, "who am I" for the keys profile "ci",
// and "login" by waiting 30 seconds, for Cancel to stop. Each call is logged.
func fakeWindowsCLI(t *testing.T) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	path, log = filepath.Join(dir, "aws.cmd"), filepath.Join(dir, "calls")
	script := strings.Join([]string{
		`@echo off`,
		`echo %*>>"` + log + `"`,
		`if "%1"=="--version" goto version`,
		`if "%1 %2"=="sts get-caller-identity" goto sts`,
		`if "%1"=="login" goto login`,
		`exit /b 2`,
		`:version`,
		`echo aws-cli/2.40.0 Python/3.12.6 Windows/10 exe/AMD64`,
		`exit /b 0`,
		`:sts`,
		`if not "%4"=="ci" goto nocreds`,
		`echo {"UserId": "AIDAEXAMPLE", "Account": "111122223333", "Arn": "arn:aws:iam::111122223333:user/ci"}`,
		`exit /b 0`,
		`:nocreds`,
		`echo Unable to locate credentials. 1>&2`,
		`exit /b 255`,
		`:login`,
		`ping -n 31 127.0.0.1 >nul`,
		`exit /b 1`,
	}, "\r\n") + "\r\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path, log
}

// The AWS CLI runs without a console window flashing up.
func TestHideConsole(t *testing.T) {
	cmd := exec.Command("aws")
	hideConsole(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("SysProcAttr %+v", cmd.SysProcAttr)
	}
}

// AWS's installer puts it under Program Files, wherever that is.
func TestAWSCLIPlacesWindows(t *testing.T) {
	t.Setenv("ProgramFiles", `D:\Apps`)
	if got := awsCLIPlaces(); len(got) != 1 || got[0] != `D:\Apps\Amazon\AWSCLIV2\aws.exe` {
		t.Fatalf("got %q", got)
	}
}

// Found through PATH (aws.cmd, by PATHEXT), its version read; then signing
// in with keys asks "who am I" and reads the answer, Windows line ends and all.
func TestWindowsCLISignIn(t *testing.T) {
	path, log := fakeWindowsCLI(t)
	if got := findAWSCLI(t.Context(), browserLoginCLI); !strings.EqualFold(got.Path, path) || got.Version != "2.40.0" {
		t.Fatalf("found %+v, want %s 2.40.0", got, path)
	}
	awsFiles(t, testConfig, testCredentials)
	id, err := (&Service{}).AWSSignIn(t.Context(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	if id != (AWSIdentity{Profile: "ci", Account: "111122223333", Arn: "arn:aws:iam::111122223333:user/ci"}) {
		t.Fatalf("got %+v", id)
	}
	if data, _ := os.ReadFile(log); !strings.Contains(string(data), "sts get-caller-identity --profile ci --output json") {
		t.Fatalf("calls: %q", data)
	}
	// Its own error comes through.
	if _, err := (&Service{}).AWSSignIn(t.Context(), "default"); err == nil {
		t.Fatal("signed in a profile the fake has no keys for")
	}
}

// Cancel while waiting for the browser stops at once on Windows too, though
// Windows has no signals: the CLI is killed, and what it started is given up on.
func TestWindowsCancelSignIn(t *testing.T) {
	isolate(t)
	fakeWindowsCLI(t)
	awsFiles(t, testConfig, testCredentials)
	s := &Service{}
	finished := make(chan error, 1)
	go func() {
		_, err := s.AWSBrowserSignIn(context.Background(), "slow", "ap-south-1")
		finished <- err
	}()
	time.Sleep(time.Second)
	s.CancelSignIn()
	select {
	case err := <-finished:
		if err == nil || err.Error() != "Sign-in cancelled." {
			t.Fatalf("got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("still waiting 10 s after Cancel")
	}
}

// Messages name the folder where Windows keeps AWS profiles.
func TestWindowsAWSFolder(t *testing.T) {
	awsFiles(t, testConfig, testCredentials)
	_, err := (&Service{}).Connect(t.Context(), "nope")
	if err == nil || !strings.Contains(err.Error(), `%USERPROFILE%\.aws`) {
		t.Fatalf("got %v", err)
	}
}
