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
	v1, v2, own := fakeAWS(t, "1.29.0"), fakeAWS(t, "2.27.6"), fakeAWS(t, "2.33.1")
	missing := filepath.Join(t.TempDir(), "aws")
	for _, c := range []struct {
		name  string
		yours []string
		own   string
		min   string
		want  AWSCLI
	}{
		{"yours, version 2", []string{missing, v2}, own, anyCLI, AWSCLI{Path: v2, Version: "2.27.6"}},
		{"yours is version 1: SpyDB's own copy", []string{v1}, own, anyCLI, AWSCLI{Path: own, Version: "2.33.1", Own: true}},
		{"none of yours: SpyDB's own copy", []string{missing}, own, anyCLI, AWSCLI{Path: own, Version: "2.33.1", Own: true}},
		{"nothing at all: install needed", []string{missing, v1}, missing, anyCLI, AWSCLI{}},
		{"a folder named aws isn't a program", []string{t.TempDir()}, "", anyCLI, AWSCLI{}},
		{"browser login: yours too old, SpyDB's own copy", []string{v2}, own, browserLoginCLI, AWSCLI{Path: own, Version: "2.33.1", Own: true}},
		{"browser login: yours too old, no copy: says which", []string{v1, v2}, missing, browserLoginCLI, AWSCLI{Older: "2.27.6"}},
		{"browser login: exactly 2.32.0 is enough", []string{fakeAWS(t, "2.32.0")}, "", browserLoginCLI, AWSCLI{Path: "", Version: "2.32.0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := pickAWSCLI(t.Context(), c.yours, c.own, c.min)
			if c.want.Path == "" && c.want.Version != "" { // the path is a temp file: check the rest
				got.Path = ""
			}
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, c := range []struct {
		version, min string
		want         bool
	}{
		{"2.32.0", "2.32.0", true},
		{"2.32.10", "2.32.9", true},
		{"2.27.6", "2.32.0", false},
		{"2.4.0", "2.32.0", false}, // compared as numbers, not text
		{"3.0.0", "2.32.0", true},
		{"2.32", "2.32.0", true},
	} {
		if got := versionAtLeast(c.version, c.min); got != c.want {
			t.Errorf("versionAtLeast(%s, %s) = %v", c.version, c.min, got)
		}
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
	if want := filepath.Join(dir, "SpyDB", "aws-cli", "aws"); path != want {
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

// The folder goes in AWS's choices.xml as text: spaces kept, & and < escaped.
func TestInstallChoices(t *testing.T) {
	xml := installChoices("/Users/me/Library/Application Support/R&D <x>")
	for _, want := range []string{
		"<string>customLocation</string>",
		"<string>/Users/me/Library/Application Support/R&amp;D &lt;x&gt;</string>",
		"<key>choiceIdentifier</key>\n      <string>default</string>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("missing %q in\n%s", want, xml)
		}
	}
}

// The real thing: download from AWS (about 60 MB), check, install into a
// temporary home, run it. Only with SPYDB_NET_TESTS=1, on a Mac.
func TestInstallAWSCLIFromAWS(t *testing.T) {
	if os.Getenv("SPYDB_NET_TESTS") == "" || runtime.GOOS != "darwin" {
		t.Skip("set SPYDB_NET_TESTS=1 on a Mac (downloads about 60 MB from AWS and installs it in a temporary folder)")
	}
	isolate(t)
	pkg, err := downloadAWSCLI(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := installAWSCLI(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	own, _ := ownAWSCLIPath()
	if cli.Path != own || !cli.Own || !versionAtLeast(cli.Version, browserLoginCLI) {
		t.Fatalf("installed %+v, want SpyDB's own copy at %s, %s or newer", cli, own, browserLoginCLI)
	}
	if _, err := os.Stat(pkg); err == nil {
		t.Errorf("the installer %s was left behind", pkg)
	}
	if got := findAWSCLI(t.Context(), browserLoginCLI); got.Path != own {
		t.Errorf("findAWSCLI for browser login picks %+v, want SpyDB's copy", got)
	}
	t.Logf("installed AWS CLI %s at %s", cli.Version, cli.Path)
}
