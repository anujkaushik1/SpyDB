package dynamo

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// awsFiles points the AWS config and credentials files at temporary ones.
func awsFiles(t *testing.T, config, credentials string) {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{"config": config, "credentials": credentials} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
}

const testConfig = `# a comment
[default]
region = ap-south-1

[profile dev]
sso_session = company
sso_account_id = 111122223333
sso_role_name = Admin
region = us-east-1

[profile legacy-sso]
SSO_START_URL = https://example.awsapps.com/start
sso_region = us-east-1

[profile ops]
role_arn = arn:aws:iam::111122223333:role/Ops
source_profile = default

[sso-session company]
sso_start_url = https://example.awsapps.com/start

[services local]
dynamodb =
  endpoint_url = http://localhost:8000
`

const testCredentials = `[default]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret-never-read-out

; another
[ci]
aws_access_key_id = AKIAEXAMPLE2
aws_secret_access_key = secret-never-read-out
`

func TestAWSProfiles(t *testing.T) {
	awsFiles(t, testConfig, testCredentials)
	profiles, err := (&Service{}).AWSProfiles()
	if err != nil {
		t.Fatal(err)
	}
	want := []AWSProfile{
		{Name: "ci", Kind: "keys"},
		{Name: "default", Kind: "keys", Region: "ap-south-1"},
		{Name: "dev", Kind: "sso", Region: "us-east-1"},
		{Name: "legacy-sso", Kind: "sso"},
		{Name: "ops", Kind: "role"},
	}
	if !slices.Equal(profiles, want) {
		t.Fatalf("got  %+v\nwant %+v", profiles, want)
	}
}

func TestAWSProfilesNoFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "none"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "none"))
	profiles, err := (&Service{}).AWSProfiles()
	if err != nil || len(profiles) != 0 {
		t.Fatalf("%v, %v", profiles, err)
	}
}

// fakeLoginCLI puts a fake aws first on PATH. It logs every call. "sso
// login" signs a profile in (any but "expired"); "who am I" works for the
// keys profile "ci" and for profiles signed in.
func fakeLoginCLI(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
case "$1 $2" in
  "--version ") echo "aws-cli/2.27.6 Python/3.13.3 Darwin/25.5.0 source/arm64" ;;
  "sso login")
    if [ "$4" = expired ]; then echo "Opening the browser..."; echo "Error when retrieving token from sso: Token has expired and refresh failed" >&2; exit 1; fi
    touch "` + dir + `/signed-in-$4"
    echo "Successfully logged into Start URL: https://example.awsapps.com/start" ;;
  "sts get-caller-identity")
    if [ "$4" != ci ] && [ ! -f "` + dir + `/signed-in-$4" ]; then echo "Error when retrieving token from sso: Token has expired and refresh failed" >&2; exit 255; fi
    echo '{"UserId": "AROAEXAMPLE:you", "Account": "111122223333", "Arn": "arn:aws:sts::111122223333:assumed-role/Admin/you"}' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	data, _ := os.ReadFile(log)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && line != "--version" {
			out = append(out, line)
		}
	}
	return out
}

func TestAWSSignIn(t *testing.T) {
	awsFiles(t, testConfig+"\n[profile expired]\nsso_session = company\n", testCredentials)
	s := &Service{}

	// An SSO profile not signed in: "who am I" fails, so the browser
	// login, then who it is.
	log := fakeLoginCLI(t)
	id, err := s.AWSSignIn(t.Context(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if id != (AWSIdentity{Profile: "dev", Account: "111122223333", Arn: "arn:aws:sts::111122223333:assumed-role/Admin/you"}) {
		t.Fatalf("got %+v", id)
	}
	want := []string{"sts get-caller-identity --profile dev --output json", "sso login --profile dev", "sts get-caller-identity --profile dev --output json"}
	if got := calls(t, log); !slices.Equal(got, want) {
		t.Fatalf("calls: %q", got)
	}

	// Signed in already: no browser this time.
	os.Remove(log)
	if _, err := s.AWSSignIn(t.Context(), "dev"); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); !slices.Equal(got, []string{"sts get-caller-identity --profile dev --output json"}) {
		t.Fatalf("calls: %q", got)
	}

	// Keys in a file: nothing to log in to, just who it is.
	log = fakeLoginCLI(t)
	if _, err := s.AWSSignIn(t.Context(), "ci"); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); !slices.Equal(got, []string{"sts get-caller-identity --profile ci --output json"}) {
		t.Fatalf("calls: %q", got)
	}

	// The CLI's own error comes through.
	fakeLoginCLI(t)
	if _, err := s.AWSSignIn(t.Context(), "expired"); err == nil || !strings.Contains(err.Error(), "Token has expired") {
		t.Fatalf("got %v", err)
	}

	// A profile that isn't in ~/.aws: refused before running anything.
	log = fakeLoginCLI(t)
	if _, err := s.AWSSignIn(t.Context(), "nope; rm -rf ~"); err == nil || len(calls(t, log)) != 0 {
		t.Fatalf("got %v, calls %q", err, calls(t, log))
	}
}
