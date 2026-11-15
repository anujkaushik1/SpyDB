package dynamo

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// AWSCLISetup is how far SpyDB has got installing its own AWS CLI, sent to
// the screen as the event awsCLISetupEvent while a sign-in waits for it.
type AWSCLISetup struct {
	Stage string `json:"stage"` // "downloading", "installing" or "done"
	Done  int64  `json:"done"`  // bytes downloaded so far
	Total int64  `json:"total"` // bytes to download; -1 if unknown
	Older string `json:"older"` // the too-old version you have, e.g. "2.27.6"; "" if none at all
}

const awsCLISetupEvent = "dynamo:aws-cli-setup"

// Registered so the screen's code knows the event's data (generated bindings).
func init() { application.RegisterEvent[AWSCLISetup](awsCLISetupEvent) }

// cliSteps are what ensureAWSCLI does; tests swap in fakes.
type cliSteps struct {
	find     func(ctx context.Context, min string) AWSCLI
	download func(ctx context.Context, progress func(done, total int64)) (string, error)
	install  func(ctx context.Context, pkg string) (AWSCLI, error)
}

// SpyDB installs its own AWS CLI only on a Mac, with AWS's installer for
// one user. Elsewhere there are no download and install steps: you install
// it (cliInstallHelp says how).
var realCLISteps = func() cliSteps {
	steps := cliSteps{find: findAWSCLI}
	if runtime.GOOS == "darwin" {
		steps.download, steps.install = downloadAWSCLI, installAWSCLI
	}
	return steps
}()

// ensureAWSCLI returns an AWS CLI of at least version min: one already there
// (yours, or SpyDB's own copy), else it downloads AWS's installer, checks
// it's Amazon's, and installs SpyDB's own copy, telling report how it goes.
// Without download steps (not a Mac) it says what to install instead.
func ensureAWSCLI(ctx context.Context, steps cliSteps, min string, report func(AWSCLISetup)) (AWSCLI, error) {
	found := steps.find(ctx, min)
	if found.Path != "" {
		return found, nil
	}
	if steps.download == nil {
		return AWSCLI{}, noAWSCLI(found.Older, min)
	}
	older := found.Older
	report(AWSCLISetup{Stage: "downloading", Total: -1, Older: older})
	var last time.Time
	pkg, err := steps.download(ctx, func(done, total int64) {
		// About ten updates a second, and always the last one: a write
		// reports every 32 KB, thousands of times for one download.
		if done != total && time.Since(last) < 100*time.Millisecond {
			return
		}
		last = time.Now()
		report(AWSCLISetup{Stage: "downloading", Done: done, Total: total, Older: older})
	})
	if err != nil {
		return AWSCLI{}, err
	}
	report(AWSCLISetup{Stage: "installing", Older: older})
	cli, err := steps.install(ctx, pkg)
	if err != nil {
		return AWSCLI{}, err
	}
	if !versionAtLeast(cli.Version, min) {
		return AWSCLI{}, fmt.Errorf("SpyDB installed the AWS CLI %s, but this needs %s or newer.", cli.Version, min)
	}
	report(AWSCLISetup{Stage: "done", Older: older})
	return cli, nil
}

// noAWSCLI is the error when no AWS CLI is new enough and SpyDB can't install
// its own: which version it needs, and how to get it.
func noAWSCLI(older, min string) error {
	if older != "" {
		return fmt.Errorf("Your AWS CLI %s is too old for this: it needs %s or newer. Update it (%s), then try again.", older, min, cliInstallHelp)
	}
	return fmt.Errorf("SpyDB signs in to AWS with the AWS CLI, and there's none on this computer. Install it (%s), then try again.", cliInstallHelp)
}

// awsCLI is ensureAWSCLI for a sign-in: one setup at a time (two sign-ins
// mustn't install over each other), its progress sent to the screen.
func (s *Service) awsCLI(ctx context.Context, min string) (AWSCLI, error) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return ensureAWSCLI(ctx, realCLISteps, min, func(setup AWSCLISetup) {
		if app := application.Get(); app != nil { // nil in tests: nobody to tell
			app.Event.Emit(awsCLISetupEvent, setup)
		}
	})
}
