package dynamo

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

[profile console]
login_session = arn:aws:iam::111122223333:user/you
region = us-west-2

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
		{Name: "console", Kind: "login", Region: "us-west-2"},
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

// fakeLoginCLI puts a fake aws (2.40.0, new enough for everything) first on
// PATH. It logs every call. "sso login" and "login" sign a profile in (any
// but "expired"; "login" also saves the profile, as aws login does); "who am
// I" works for the keys profile "ci" and for profiles signed in.
func fakeLoginCLI(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
case "$1 $2" in
  "--version ") echo "aws-cli/2.40.0 Python/3.14.8 Darwin/25.5.0 exe/arm64" ;;
  "login --profile")
    if [ "$3" = slow ]; then sleep 30; fi
    if [ "$3" = expired ]; then echo "Attempting to open your default browser."; echo "Login was not completed" >&2; exit 1; fi
    touch "` + dir + `/signed-in-$3"
    printf '\n[profile %s]\nlogin_session = arn:aws:iam::111122223333:user/you\nregion = %s\n' "$3" "$5" >> "$AWS_CONFIG_FILE" ;;
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

// Signing in with your AWS console account through the browser: aws login,
// saved as its own profile, then who that is. Next time, no browser while
// it lasts; after it expires, the browser again.
func TestAWSBrowserSignIn(t *testing.T) {
	isolate(t)
	awsFiles(t, testConfig, testCredentials)
	s := &Service{}
	log := fakeLoginCLI(t)

	id, err := s.AWSBrowserSignIn(t.Context(), "spydb", "ap-south-1")
	if err != nil {
		t.Fatal(err)
	}
	if id.Profile != "spydb" || id.Account != "111122223333" {
		t.Fatalf("got %+v", id)
	}
	want := []string{"login --profile spydb --region ap-south-1", "sts get-caller-identity --profile spydb --output json"}
	if got := calls(t, log); !slices.Equal(got, want) {
		t.Fatalf("calls: %q", got)
	}
	profiles, _ := s.AWSProfiles()
	if !slices.Contains(profiles, AWSProfile{Name: "spydb", Kind: "login", Region: "ap-south-1"}) {
		t.Fatalf("the new profile isn't listed: %+v", profiles)
	}

	// Signing in with it again while it lasts: no browser.
	os.Remove(log)
	if _, err := s.AWSSignIn(t.Context(), "spydb"); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); !slices.Equal(got, []string{"sts get-caller-identity --profile spydb --output json"}) {
		t.Fatalf("calls: %q", got)
	}

	// Expired: the browser again, in the profile's region.
	os.Remove(log)
	os.Remove(filepath.Join(filepath.Dir(log), "signed-in-spydb"))
	if _, err := s.AWSSignIn(t.Context(), "spydb"); err != nil {
		t.Fatal(err)
	}
	want = []string{"sts get-caller-identity --profile spydb --output json", "login --profile spydb --region ap-south-1", "sts get-caller-identity --profile spydb --output json"}
	if got := calls(t, log); !slices.Equal(got, want) {
		t.Fatalf("calls: %q", got)
	}
}

func TestAWSBrowserSignInRefuses(t *testing.T) {
	isolate(t)
	awsFiles(t, testConfig, testCredentials)
	s := &Service{}
	log := fakeLoginCLI(t)
	for _, c := range []struct{ profile, region, why string }{
		{"my profile", "ap-south-1", "can't be a profile name"},
		{"spydb", "mumbai", "isn't an AWS region"},
		{"ci", "ap-south-1", "already signs in another way (keys)"},
		{"dev", "ap-south-1", "already signs in another way (sso)"},
	} {
		if _, err := s.AWSBrowserSignIn(t.Context(), c.profile, c.region); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s/%s: %v", c.profile, c.region, err)
		}
	}
	if got := calls(t, log); len(got) != 0 {
		t.Fatalf("ran %q", got)
	}
	// Not finishing in the browser: AWS's reason, nothing saved.
	if _, err := s.AWSBrowserSignIn(t.Context(), "expired", "ap-south-1"); err == nil || !strings.Contains(err.Error(), "Login was not completed") {
		t.Fatalf("got %v", err)
	}
}

// Cancel while waiting for the browser: the sign-in ends at once, saying so,
// and nothing is saved.
func TestCancelSignIn(t *testing.T) {
	isolate(t)
	awsFiles(t, testConfig, testCredentials)
	s := &Service{}
	fakeLoginCLI(t)
	finished := make(chan error, 1)
	go func() {
		_, err := s.AWSBrowserSignIn(context.Background(), "slow", "ap-south-1")
		finished <- err
	}()
	time.Sleep(500 * time.Millisecond)
	s.CancelSignIn()
	select {
	case err := <-finished:
		if err == nil || err.Error() != "Sign-in cancelled." {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting 5 s after Cancel")
	}
	profiles, _ := s.AWSProfiles()
	if slices.ContainsFunc(profiles, func(p AWSProfile) bool { return p.Name == "slow" }) {
		t.Fatal("a cancelled sign-in was saved")
	}
	s.CancelSignIn() // nothing running: nothing happens
}

// A profile with nothing to sign in with isn't offered; one whose keys come
// from a program (credential_process) is. Inside, both are still known, so a
// browser sign-in can still tell a name is taken.
func TestAWSProfilesWithoutCredentials(t *testing.T) {
	awsFiles(t, `[default]
region = ap-south-1

[profile vault]
credential_process = /usr/local/bin/aws-vault export --format=json work

[profile half]
region = us-east-1
`, `[ci]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret-never-read-out
`)
	profiles, err := (&Service{}).AWSProfiles()
	want := []AWSProfile{{Name: "ci", Kind: "keys"}, {Name: "vault", Kind: "process"}}
	if err != nil || !slices.Equal(profiles, want) {
		t.Fatalf("got %+v, %v; want %+v", profiles, err, want)
	}
	all, err := awsProfiles()
	if err != nil || len(all) != 4 {
		t.Fatalf("inside: %+v, %v", all, err)
	}
}
