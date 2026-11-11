package dynamo

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
)

// fakeSteps finds found, "downloads" calling progress for every 1 KB of a
// 1 MB file, and "installs" giving installed; it records what ran.
func fakeSteps(found, installed AWSCLI, downloadErr, installErr error, ran *[]string) cliSteps {
	return cliSteps{
		find: func(context.Context, string) AWSCLI { *ran = append(*ran, "find"); return found },
		download: func(_ context.Context, progress func(done, total int64)) (string, error) {
			*ran = append(*ran, "download")
			if downloadErr != nil {
				return "", downloadErr
			}
			for done := int64(1024); done <= 1<<20; done += 1024 {
				progress(done, 1<<20)
			}
			return "/tmp/AWSCLIV2.pkg", nil
		},
		install: func(_ context.Context, pkg string) (AWSCLI, error) {
			*ran = append(*ran, "install "+pkg)
			return installed, installErr
		},
	}
}

func TestEnsureAWSCLI(t *testing.T) {
	yours := AWSCLI{Path: "/opt/homebrew/bin/aws", Version: "2.33.0"}
	ours := AWSCLI{Path: "/x/SpyDB/aws-cli/aws", Version: "2.40.0", Own: true}

	t.Run("one new enough is there: nothing to install", func(t *testing.T) {
		var ran []string
		var reports []AWSCLISetup
		cli, err := ensureAWSCLI(t.Context(), fakeSteps(yours, ours, nil, nil, &ran), browserLoginCLI, func(s AWSCLISetup) { reports = append(reports, s) })
		if err != nil || cli != yours || len(ran) != 1 || len(reports) != 0 {
			t.Fatalf("got %+v, %v; ran %q; reports %+v", cli, err, ran, reports)
		}
	})

	t.Run("yours too old: download, install, report as it goes", func(t *testing.T) {
		var ran []string
		var reports []AWSCLISetup
		cli, err := ensureAWSCLI(t.Context(), fakeSteps(AWSCLI{Older: "2.27.6"}, ours, nil, nil, &ran), browserLoginCLI, func(s AWSCLISetup) { reports = append(reports, s) })
		if err != nil || cli != ours {
			t.Fatalf("got %+v, %v", cli, err)
		}
		if len(ran) != 3 || ran[1] != "download" || ran[2] != "install /tmp/AWSCLIV2.pkg" {
			t.Fatalf("ran %q", ran)
		}
		first, last := reports[0], reports[len(reports)-1]
		if first != (AWSCLISetup{Stage: "downloading", Total: -1, Older: "2.27.6"}) || last != (AWSCLISetup{Stage: "done", Older: "2.27.6"}) {
			t.Fatalf("first %+v, last %+v", first, last)
		}
		installing := reports[len(reports)-2]
		finished := reports[len(reports)-3]
		if installing.Stage != "installing" || finished != (AWSCLISetup{Stage: "downloading", Done: 1 << 20, Total: 1 << 20, Older: "2.27.6"}) {
			t.Fatalf("before installing: %+v, then %+v", finished, installing)
		}
		// 1,024 progress calls in a moment: a few reports, not a thousand.
		if len(reports) > 20 {
			t.Errorf("%d reports for one quick download", len(reports))
		}
	})

	t.Run("download fails: no install, its reason", func(t *testing.T) {
		var ran []string
		_, err := ensureAWSCLI(t.Context(), fakeSteps(AWSCLI{}, ours, errors.New("no internet"), nil, &ran), anyCLI, func(AWSCLISetup) {})
		if err == nil || err.Error() != "no internet" || len(ran) != 2 {
			t.Fatalf("%v; ran %q", err, ran)
		}
	})

	t.Run("installed, but older than needed: says so", func(t *testing.T) {
		var ran []string
		_, err := ensureAWSCLI(t.Context(), fakeSteps(AWSCLI{}, AWSCLI{Path: "/x/aws", Version: "2.31.0", Own: true}, nil, nil, &ran), browserLoginCLI, func(AWSCLISetup) {})
		if err == nil {
			t.Fatal("accepted 2.31.0 for the browser login")
		}
	})

	t.Run("install fails: its reason", func(t *testing.T) {
		var ran []string
		_, err := ensureAWSCLI(t.Context(), fakeSteps(AWSCLI{}, AWSCLI{}, nil, errors.New("disk full"), &ran), anyCLI, func(AWSCLISetup) {})
		if err == nil || err.Error() != "disk full" {
			t.Fatalf("%v; ran %q", err, ran)
		}
	})
}

// The real thing: yours (if any) is too old for the browser login, so SpyDB
// downloads from AWS and installs its own into a temporary home. Only with
// SPYDB_NET_TESTS=1, on a Mac.
func TestEnsureAWSCLIFromAWS(t *testing.T) {
	if os.Getenv("SPYDB_NET_TESTS") == "" || runtime.GOOS != "darwin" {
		t.Skip("set SPYDB_NET_TESTS=1 on a Mac (downloads about 60 MB from AWS and installs it in a temporary folder)")
	}
	isolate(t)
	if found := findAWSCLI(t.Context(), browserLoginCLI); found.Path != "" {
		t.Skipf("%s is already new enough: nothing to install", found.Path)
	}
	var stages []string
	cli, err := ensureAWSCLI(t.Context(), realCLISteps, browserLoginCLI, func(s AWSCLISetup) {
		if len(stages) == 0 || stages[len(stages)-1] != s.Stage {
			stages = append(stages, s.Stage)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cli.Own || len(stages) != 3 || stages[0] != "downloading" || stages[1] != "installing" || stages[2] != "done" {
		t.Fatalf("got %+v, stages %q", cli, stages)
	}
	t.Logf("installed %s at %s; stages %q", cli.Version, cli.Path, stages)
}
