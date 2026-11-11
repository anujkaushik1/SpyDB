package dynamo

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fakeAWS writes a script that prints what `aws --version` would.
func fakeAWS(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aws")
	script := "#!/bin/sh\necho 'aws-cli/" + version + " Python/3.13.3 Darwin/25.5.0 source/arm64'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPickAWSCLI(t *testing.T) {
	v1, v2, own := fakeAWS(t, "1.29.0"), fakeAWS(t, "2.27.6"), fakeAWS(t, "2.31.0")
	missing := filepath.Join(t.TempDir(), "aws")
	for _, c := range []struct {
		name  string
		yours []string
		own   string
		want  AWSCLI
	}{
		{"yours, version 2", []string{missing, v2}, own, AWSCLI{Path: v2, Version: "2.27.6"}},
		{"yours is version 1: SpyDB's own copy", []string{v1}, own, AWSCLI{Path: own, Version: "2.31.0", Own: true}},
		{"none of yours: SpyDB's own copy", []string{missing}, own, AWSCLI{Path: own, Version: "2.31.0", Own: true}},
		{"nothing at all: install needed", []string{missing, v1}, missing, AWSCLI{}},
		{"a folder named aws isn't a program", []string{t.TempDir()}, "", AWSCLI{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := pickAWSCLI(t.Context(), c.yours, c.own); got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// SpyDB's own copy lives in its settings folder.
func TestOwnAWSCLIPath(t *testing.T) {
	isolate(t)
	path, err := ownAWSCLIPath()
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := os.UserConfigDir()
	if want := filepath.Join(dir, "SpyDB", "aws-cli", "bin", "aws"); path != want {
		t.Fatalf("got %s, want %s", path, want)
	}
}

// What pkgutil reported for the genuine AWSCLIV2.pkg (November 2026).
const genuinePkgReport = `Package "AWSCLIV2.pkg":
   Status: signed by a developer certificate issued by Apple for distribution
   Notarization: trusted by the Apple notary service
   Signed with a trusted timestamp on: 2026-11-10 19:12:50 +0000
   Certificate Chain:
    1. Developer ID Installer: AMZN Mobile LLC (94KV3E626L)
       Expires: 2030-09-26 00:18:06 +0000
    2. Developer ID Certification Authority
    3. Apple Root CA
`

func TestPkgSignatureProblem(t *testing.T) {
	if err := pkgSignatureProblem(genuinePkgReport); err != nil {
		t.Fatalf("Amazon's installer refused: %v", err)
	}
	for name, report := range map[string]string{
		"another developer":           strings.Replace(genuinePkgReport, "AMZN Mobile LLC (94KV3E626L)", "Someone Else (ABCDE12345)", 1),
		"Amazon's name, another team": strings.Replace(genuinePkgReport, "(94KV3E626L)", "(ABCDE12345)", 1),
		"not notarized":               strings.Replace(genuinePkgReport, "Notarization: trusted by the Apple notary service\n", "", 1),
		"not signed":                  "Package \"AWSCLIV2.pkg\":\n   Status: no signature\n",
	} {
		if pkgSignatureProblem(report) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDownload(t *testing.T) {
	body := strings.Repeat("x", 100_000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			io.WriteString(w, body)
		case "/short": // says 100,000 bytes, sends 10
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			io.WriteString(w, body[:10])
		case "/huge":
			w.Header().Set("Content-Length", strconv.Itoa(maxAWSCLIDownload+1))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()

	var last, total int64
	path := filepath.Join(dir, "ok.pkg")
	if err := download(t.Context(), server.URL+"/ok", path, func(done, all int64) { last, total = done, all }); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != body || last != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("got %d bytes, progress %d of %d", len(got), last, total)
	}
	for _, name := range []string{"short", "huge", "missing"} {
		path := filepath.Join(dir, name+".pkg")
		if err := download(t.Context(), server.URL+"/"+name, path, nil); err == nil {
			t.Errorf("%s: downloaded", name)
		}
		for _, leftover := range []string{path, path + ".part"} {
			if _, err := os.Stat(leftover); err == nil {
				t.Errorf("%s: %s left behind", name, filepath.Base(leftover))
			}
		}
	}
}

// The real thing, from AWS (about 60 MB): only with SPYDB_NET_TESTS=1, on a Mac.
func TestDownloadAWSCLIFromAWS(t *testing.T) {
	if os.Getenv("SPYDB_NET_TESTS") == "" || runtime.GOOS != "darwin" {
		t.Skip("set SPYDB_NET_TESTS=1 on a Mac (downloads about 60 MB from AWS)")
	}
	isolate(t)
	path, err := downloadAWSCLI(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() < 20<<20 {
		t.Fatalf("%s: %v, %d bytes", path, err, info.Size())
	}
	t.Logf("downloaded and verified %s (%d MB)", path, info.Size()>>20)
}

// isolate points the settings folder at a temporary home for one test.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
}
