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
	"slices"
	"strings"
	"sync"
)

// Service is what the app's screens call for DynamoDB.
type Service struct {
	setupMu sync.Mutex // one AWS CLI setup at a time (see awsCLI)
}

// AWSProfile is a profile from ~/.aws/config or ~/.aws/credentials: its name
// and how it gets credentials. Keys and other settings are never read out.
type AWSProfile struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`   // "sso": sign in through the browser; "keys": access keys in a file; "role": assumes a role; "other"
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

// AWSSignIn signs in to AWS with a profile and says who that is. An SSO
// profile's temporary credentials last some hours: if they still work,
// that's it; otherwise it runs the AWS CLI's browser login (aws sso login),
// which opens AWS's sign-in page and waits until you've signed in there. A
// profile with keys needs no login. With no AWS CLI version 2 at all, SpyDB
// first installs its own (ensureAWSCLI). The CLI keeps the credentials in
// ~/.aws; SpyDB stores nothing.
func (s *Service) AWSSignIn(ctx context.Context, profile string) (AWSIdentity, error) {
	profiles, err := awsProfiles()
	if err != nil {
		return AWSIdentity{}, err
	}
	i := slices.IndexFunc(profiles, func(p AWSProfile) bool { return p.Name == profile })
	if i < 0 {
		return AWSIdentity{}, fmt.Errorf("There's no AWS profile named %q in ~/.aws.", profile)
	}
	cli, err := s.awsCLI(ctx, anyCLI) // none: SpyDB installs its own
	if err != nil {
		return AWSIdentity{}, err
	}
	if profiles[i].Kind == "sso" {
		// Signed in recently: the credentials still work, no browser.
		if id, err := awsIdentity(ctx, cli.Path, profile); err == nil {
			return id, nil
		}
		if out, err := exec.CommandContext(ctx, cli.Path, "sso", "login", "--profile", profile).CombinedOutput(); err != nil {
			if ctx.Err() != nil {
				return AWSIdentity{}, errors.New("Sign-in cancelled.")
			}
			return AWSIdentity{}, fmt.Errorf("AWS sign-in failed: %s", lastLine(out))
		}
	}
	return awsIdentity(ctx, cli.Path, profile)
}

// awsIdentity asks AWS who the profile is signed in as.
func awsIdentity(ctx context.Context, cli, profile string) (AWSIdentity, error) {
	cmd := exec.CommandContext(ctx, cli, "sts", "get-caller-identity", "--profile", profile, "--output", "json")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return AWSIdentity{}, fmt.Errorf("Signed in, but AWS didn't accept the credentials: %s", lastLine(exit.Stderr))
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
		case "role_arn":
			if p.Kind != "sso" {
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
