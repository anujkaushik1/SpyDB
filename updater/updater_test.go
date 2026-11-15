package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.1.7", "0.1.6", true},
		{"0.1.10", "0.1.9", true}, // numbers, not text
		{"0.2", "0.1.9", true},
		{"0.1.6", "0.1.6", false},
		{"0.1.6.0", "0.1.6", false},
		{"0.1.6", "0.1.7", false},
		{"1.0.0", "0.99.99", true},
		{"", "0.1.6", false},
		{"0.1.7", "", false},
		{"0.1.7-beta", "0.1.6", false},
		{"v0.1.7", "0.1.6", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeGitHub answers as GitHub does: the latest-release page redirects to
// the release's tag, and policy is updates.json ("" for none: 404).
func fakeGitHub(t *testing.T, latest, policy string) *Service {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "https://github.com/anujkaushik1/SpyDB/releases/tag/v"+latest, http.StatusFound)
		case "/updates.json":
			if policy == "" {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(policy))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Service{LatestURL: srv.URL + "/releases/latest", PolicyURL: srv.URL + "/updates.json", DownloadURL: srv.URL + "/download"}
}

func TestCheck(t *testing.T) {
	if platform == "" {
		t.Skip("no updates on this platform")
	}
	other := map[string]string{"mac": "ubuntu", "ubuntu": "mac"}[platform]
	minimum := func(p, v string) string { return `{"minimum": {"` + p + `": "` + v + `"}}` }
	for _, c := range []struct {
		name                         string
		current, latest, policy      string
		latestSeen                   string
		available, required, install bool
	}{
		{"newer release", "0.1.6", "0.1.7", "", "0.1.7", true, false, true},
		{"up to date", "0.1.7", "0.1.7", "", "0.1.7", false, false, false},
		{"ahead of the release (a local build)", "0.1.8", "0.1.7", "", "0.1.7", false, false, false},
		{"required here", "0.1.6", "0.1.7", minimum(platform, "0.1.7"), "0.1.7", true, true, true},
		{"required from an older version", "0.1.4", "0.1.7", minimum(platform, "0.1.5"), "0.1.7", true, true, true},
		{"minimum met already", "0.1.6", "0.1.7", minimum(platform, "0.1.6"), "0.1.7", true, false, true},
		{"required on the other platform only", "0.1.6", "0.1.7", minimum(other, "0.1.7"), "0.1.7", true, false, true},
		{"minimum's release not out yet", "0.1.6", "0.1.7", minimum(platform, "0.1.8"), "0.1.7", true, false, true},
		{"updates.json unreadable", "0.1.6", "0.1.7", "{not json", "0.1.7", true, false, true},
		{"a dev build", "", "0.1.7", minimum(platform, "0.1.7"), "", false, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := fakeGitHub(t, c.latest, c.policy)
			inst := writableInstallation(t)
			// On Linux without root or pkexec SpyDB can't install (TestInstallUbuntu covers that).
			wantInstall := c.install && inst.problem() == ""
			st := s.check(t.Context(), inst, c.current)
			if st.Latest != c.latestSeen || st.Available != c.available || st.Required != c.required || st.CanInstall != wantInstall {
				t.Errorf("got latest %q available %v required %v canInstall %v (%q); want %q %v %v %v",
					st.Latest, st.Available, st.Required, st.CanInstall, st.Problem, c.latestSeen, c.available, c.required, wantInstall)
			}
			if st.Platform != platform || st.Command != manualCommand {
				t.Errorf("platform %q command %q", st.Platform, st.Command)
			}
		})
	}
}

// Offline (or GitHub down): nothing is available, nothing required.
func TestCheckOffline(t *testing.T) {
	if platform == "" {
		t.Skip("no updates on this platform")
	}
	s := fakeGitHub(t, "0.1.7", `{"minimum": {"mac": "9.9.9", "ubuntu": "9.9.9"}}`)
	s.LatestURL = "http://127.0.0.1:1/releases/latest"
	st := s.check(t.Context(), writableInstallation(t), "0.1.6")
	if st.Latest != "" || st.Available || st.Required {
		t.Errorf("offline: %+v", st)
	}
	// A release page that doesn't redirect to a version.
	s = fakeGitHub(t, "x", "")
	if st := s.check(t.Context(), writableInstallation(t), "0.1.6"); st.Latest != "" || st.Available {
		t.Errorf("no version: %+v", st)
	}
}

// A download that stops arriving ends with an error, not a window stuck on
// "Downloading…"; so does one cancelled from the window.
func TestDownloadStalls(t *testing.T) {
	stallTimeout = 300 * time.Millisecond
	t.Cleanup(func() { stallTimeout = 30 * time.Second })
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write(make([]byte, 100))
		w.(http.Flusher).Flush()
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); srv.Close() })
	path := filepath.Join(t.TempDir(), "file")

	var got []Progress
	err := download(t.Context(), srv.URL, path, func(p Progress) { got = append(got, p) })
	if err == nil || !strings.Contains(err.Error(), "nothing arrived for 300ms") {
		t.Errorf("stalled download: %v", err)
	}
	if len(got) != 1 || got[0] != (Progress{"download", 10}) {
		t.Errorf("progress %v", got)
	}

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	if err := download(ctx, srv.URL, path, func(Progress) {}); err == nil || strings.Contains(err.Error(), "nothing arrived") {
		t.Errorf("cancelled download: %v", err)
	}
}
