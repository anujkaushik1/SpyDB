// Package dynamo is SpyDB's DynamoDB side: signing in to AWS (with the AWS
// CLI's browser login) and, later, tables and items. The app's screens call
// it through Service.
package dynamo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Service is what the app's screens call for DynamoDB.
type Service struct {
	setupMu sync.Mutex // one AWS CLI setup at a time (see awsCLI)

	mu           sync.Mutex         // guards the fields below
	cancelSignIn context.CancelFunc // stops the sign-in running, if any (CancelSignIn)
	signInSeq    int                // counts sign-ins, so one ending clears only its own cancel
}

// CancelSignIn stops the sign-in running: downloading or installing the AWS
// CLI, or waiting for you in the browser. That sign-in then ends with "Sign-in
// cancelled." (Stopping it here, rather than abandoning the call on the
// screen, lets it end normally.)
func (s *Service) CancelSignIn() {
	s.mu.Lock()
	cancel := s.cancelSignIn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// cancellable is ctx for a sign-in that CancelSignIn can stop; call done when it ends.
func (s *Service) cancellable(ctx context.Context) (_ context.Context, done func()) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.signInSeq++
	seq := s.signInSeq
	s.cancelSignIn = cancel
	s.mu.Unlock()
	return ctx, func() {
		s.mu.Lock()
		if s.signInSeq == seq {
			s.cancelSignIn = nil
		}
		s.mu.Unlock()
		cancel()
	}
}

// cancelledOr is err, or "Sign-in cancelled." when the sign-in was stopped.
func cancelledOr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return errors.New("Sign-in cancelled.")
	}
	return err
}

// AWSProfile is a profile from ~/.aws/config or ~/.aws/credentials: its name
// and how it gets credentials. Keys and other settings are never read out.
type AWSProfile struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`   // "keys": access keys in a file; "sso": your company's AWS sign-in; "login": your AWS console sign-in (aws login); "role": assumes a role; "other"
	Region string `json:"region"` // its default region, e.g. "ap-south-1"; "" if none
}

// AWSIdentity is who a profile signs in as (aws sts get-caller-identity).
type AWSIdentity struct {
	Profile string `json:"profile"`
	Account string `json:"account"` // the AWS account number
	Arn     string `json:"arn"`     // e.g. arn:aws:sts::123456789012:assumed-role/Admin/you@example.com
}

// AWSProfiles lists your AWS profiles, sorted by name.
func (s *Service) AWSProfiles() ([]AWSProfile, error) {
	return awsProfiles()
}

// AWSSignIn signs in to AWS with a profile and says who that is. A profile
// with keys needs no login. SSO and console sign-in profiles get temporary
// credentials lasting some hours: if they still work, that's it; otherwise
// it runs the AWS CLI's browser login (aws sso login, or aws login), which
// opens AWS's sign-in page and waits until you've signed in there. When the
// AWS CLI is missing or too old for that, SpyDB first installs its own
// (ensureAWSCLI). The CLI keeps the credentials in ~/.aws; SpyDB stores
// nothing.
func (s *Service) AWSSignIn(ctx context.Context, profile string) (AWSIdentity, error) {
	ctx, done := s.cancellable(ctx)
	defer done()
	id, err := s.signIn(ctx, profile)
	return id, cancelledOr(ctx, err)
}

func (s *Service) signIn(ctx context.Context, profile string) (AWSIdentity, error) {
	profiles, err := awsProfiles()
	if err != nil {
		return AWSIdentity{}, err
	}
	i := slices.IndexFunc(profiles, func(p AWSProfile) bool { return p.Name == profile })
	if i < 0 {
		return AWSIdentity{}, fmt.Errorf("There's no AWS profile named %q in ~/.aws.", profile)
	}
	kind := profiles[i].Kind
	min := anyCLI
	if kind == "login" {
		min = browserLoginCLI // older CLIs can't even read these credentials
	}
	cli, err := s.awsCLI(ctx, min) // none new enough: SpyDB installs its own
	if err != nil {
		return AWSIdentity{}, err
	}
	if kind == "login" {
		if id, err := awsIdentity(ctx, cli.Path, profile); err == nil {
			return id, nil // signed in within the last 12 hours
		}
		region := profiles[i].Region
		if region == "" {
			region = "us-east-1"
		}
		if err := awsLogin(ctx, cli.Path, profile, region); err != nil {
			return AWSIdentity{}, err
		}
	}
	if kind == "sso" {
		// Signed in recently: the credentials still work, no browser.
		if id, err := awsIdentity(ctx, cli.Path, profile); err == nil {
			return id, nil
		}
		if out, err := awsCommand(ctx, cli.Path, "sso", "login", "--profile", profile).CombinedOutput(); err != nil {
			if ctx.Err() != nil {
				return AWSIdentity{}, errors.New("Sign-in cancelled.")
			}
			return AWSIdentity{}, fmt.Errorf("AWS sign-in failed: %s", lastLine(out))
		}
	}
	return awsIdentity(ctx, cli.Path, profile)
}

// AWSBrowserSignIn signs in with your AWS console account (the one you use
// on aws.amazon.com) through your browser, saving it as the profile named
// (SpyDB uses "spydb"), with region as its region: aws login, which needs AWS
// CLI 2.32 or newer, so SpyDB first installs its own if yours is older. The
// credentials last up to 12 hours; signing in with that profile again after
// that opens the browser again (AWSSignIn).
func (s *Service) AWSBrowserSignIn(ctx context.Context, profile, region string) (AWSIdentity, error) {
	ctx, done := s.cancellable(ctx)
	defer done()
	id, err := s.browserSignIn(ctx, profile, region)
	return id, cancelledOr(ctx, err)
}

func (s *Service) browserSignIn(ctx context.Context, profile, region string) (AWSIdentity, error) {
	if !profileName.MatchString(profile) {
		return AWSIdentity{}, fmt.Errorf("%q can't be a profile name: use letters, digits, - _ and . only.", profile)
	}
	if !regionName.MatchString(region) {
		return AWSIdentity{}, fmt.Errorf("%q isn't an AWS region, like ap-south-1.", region)
	}
	// AWS: keys in a profile win over its console sign-in, and an SSO
	// profile signs in another way: don't mix them in one profile.
	profiles, err := awsProfiles()
	if err != nil {
		return AWSIdentity{}, err
	}
	if i := slices.IndexFunc(profiles, func(p AWSProfile) bool { return p.Name == profile }); i >= 0 && profiles[i].Kind != "login" && profiles[i].Kind != "other" {
		return AWSIdentity{}, fmt.Errorf("The profile %q already signs in another way (%s). Pick another name.", profile, profiles[i].Kind)
	}
	cli, err := s.awsCLI(ctx, browserLoginCLI)
	if err != nil {
		return AWSIdentity{}, err
	}
	if err := awsLogin(ctx, cli.Path, profile, region); err != nil {
		return AWSIdentity{}, err
	}
	return awsIdentity(ctx, cli.Path, profile)
}

var (
	profileName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	regionName  = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)
)

// awsLogin runs aws login, which opens AWS's sign-in page in your browser
// and waits for it to call back. With no terminal to answer questions, the
// region always goes on the command line (without it, the CLI asks and
// fails); nothing is saved unless you finish signing in.
func awsLogin(ctx context.Context, cli, profile, region string) error {
	out, err := awsCommand(ctx, cli, "login", "--profile", profile, "--region", region).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("Sign-in cancelled.")
		}
		return fmt.Errorf("AWS sign-in failed: %s", lastLine(out))
	}
	return nil
}

// awsCommand runs the AWS CLI so that cancelling ctx stops it promptly: it's
// killed, and anything it started that still holds its output is given
// up on after a moment, instead of waited for.
func awsCommand(ctx context.Context, cli string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// awsIdentity asks AWS who the profile is signed in as.
func awsIdentity(ctx context.Context, cli, profile string) (AWSIdentity, error) {
	cmd := awsCommand(ctx, cli, "sts", "get-caller-identity", "--profile", profile, "--output", "json")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return AWSIdentity{}, fmt.Errorf("AWS couldn't confirm who this profile is: %s", lastLine(exit.Stderr))
		}
		return AWSIdentity{}, err
	}
	var id struct{ Account, Arn string }
	if err := json.Unmarshal(out, &id); err != nil {
		return AWSIdentity{}, fmt.Errorf("Couldn't read AWS's answer: %w", err)
	}
	return AWSIdentity{Profile: profile, Account: id.Account, Arn: id.Arn}, nil
}

// lastLine is the last non-empty line of a program's output: the AWS CLI
// puts its error there.
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// awsProfiles reads the profiles in ~/.aws/config and ~/.aws/credentials
// (or the files AWS_CONFIG_FILE and AWS_SHARED_CREDENTIALS_FILE name, as
// the AWS CLI does). Missing files just mean no profiles.
func awsProfiles() ([]AWSProfile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	configFile := os.Getenv("AWS_CONFIG_FILE")
	if configFile == "" {
		configFile = filepath.Join(home, ".aws", "config")
	}
	credentialsFile := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	if credentialsFile == "" {
		credentialsFile = filepath.Join(home, ".aws", "credentials")
	}

	byName := map[string]*AWSProfile{}
	profile := func(name string) *AWSProfile {
		if byName[name] == nil {
			byName[name] = &AWSProfile{Name: name, Kind: "other"}
		}
		return byName[name]
	}
	// In config, profiles are [default] and [profile name]; other sections
	// ([sso-session x], [services x]) aren't profiles.
	err = readINI(configFile, func(section, key, value string) {
		var name string
		switch {
		case section == "default":
			name = "default"
		case strings.HasPrefix(section, "profile "):
			name = strings.TrimSpace(strings.TrimPrefix(section, "profile "))
		default:
			return
		}
		p := profile(name)
		switch key {
		case "":
		case "sso_session", "sso_start_url":
			p.Kind = "sso"
		case "login_session": // written by aws login
			if p.Kind != "sso" {
				p.Kind = "login"
			}
		case "role_arn":
			if p.Kind != "sso" && p.Kind != "login" {
				p.Kind = "role"
			}
		case "aws_access_key_id":
			if p.Kind == "other" {
				p.Kind = "keys"
			}
		case "region":
			p.Region = value
		}
	})
	if err != nil {
		return nil, err
	}
	// In credentials, every section is a profile name.
	err = readINI(credentialsFile, func(section, key, _ string) {
		p := profile(section)
		if key == "aws_access_key_id" && p.Kind == "other" {
			p.Kind = "keys"
		}
	})
	if err != nil {
		return nil, err
	}

	profiles := make([]AWSProfile, 0, len(byName))
	for _, p := range byName {
		profiles = append(profiles, *p)
	}
	slices.SortFunc(profiles, func(a, b AWSProfile) int { return strings.Compare(a.Name, b.Name) })
	return profiles, nil
}

// readINI calls each for every key in an INI file (and once with key "" for
// every section), keys in lower case. Only the keys SpyDB asks about are
// looked at; a missing file is no error.
func readINI(path string, each func(section, key, value string)) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	section := ""
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
		case line[0] == '[' && strings.HasSuffix(line, "]"):
			section = strings.TrimSpace(line[1 : len(line)-1])
			each(section, "", "")
		case section != "":
			if key, value, ok := strings.Cut(line, "="); ok {
				each(section, strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value))
			}
		}
	}
	return lines.Err()
}
