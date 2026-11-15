// Package updater checks GitHub for a newer SpyDB release when the app
// opens, and installs it: the Mac app in place, the Ubuntu package with apt
// (the system asks for the password), or on Windows the installer, once
// SpyDB has quit. Which versions must update is in updates.json on main,
// per platform, so a Mac-only fix can be required on Macs without being
// required on Ubuntu or Windows.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const repo = "https://github.com/anujkaushik1/SpyDB"

// progressEvent carries Install's Progress to the window.
const progressEvent = "spydb:update-progress"

// checkTimeout limits Check: an unanswered GitHub must not hold up anything.
const checkTimeout = 8 * time.Second

// Status is what Check found.
type Status struct {
	Platform   string `json:"platform"`   // "mac", "ubuntu" or "windows"; "" where SpyDB doesn't update itself
	Current    string `json:"current"`    // this copy's version; "" if it isn't installed from a release (a dev build)
	Latest     string `json:"latest"`     // the newest release; "" if GitHub couldn't be asked
	Available  bool   `json:"available"`  // Latest is newer than Current
	Required   bool   `json:"required"`   // Current is below updates.json's minimum for this platform: no "Later"
	CanInstall bool   `json:"canInstall"` // SpyDB can install it itself
	Problem    string `json:"problem"`    // why it can't, when it can't
	Command    string `json:"command"`    // the Terminal command that installs it instead
}

// Progress is how far Install has got.
type Progress struct {
	Step    string `json:"step"`    // "download", then "install", then "restart"
	Percent int    `json:"percent"` // of the download
}

// Service is bound to the window. The zero value asks GitHub; tests point
// the URLs at a server of their own.
type Service struct {
	LatestURL   string // redirects to the newest release's page, …/releases/tag/v1.2.3
	PolicyURL   string // updates.json
	DownloadURL string // release files are at DownloadURL/v1.2.3/<file>

	installing sync.Mutex
}

func (s *Service) urls() (latest, policy, download string) {
	latest, policy, download = s.LatestURL, s.PolicyURL, s.DownloadURL
	if latest == "" {
		latest = repo + "/releases/latest"
	}
	if policy == "" {
		policy = "https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/updates.json"
	}
	if download == "" {
		download = repo + "/releases/download"
	}
	return latest, policy, download
}

// Check asks GitHub for the newest release and updates.json. Offline, or for
// a dev build, it finds nothing available.
func (s *Service) Check(ctx context.Context) Status {
	inst, current := installed()
	return s.check(ctx, inst, current)
}

func (s *Service) check(ctx context.Context, inst installation, current string) Status {
	st := Status{Platform: platform, Current: current, Command: manualCommand}
	if platform == "" || current == "" {
		return st
	}
	latestURL, policyURL, _ := s.urls()
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	var minimum string
	var wg sync.WaitGroup
	wg.Go(func() { minimum = fetchMinimum(ctx, policyURL)[platform] })
	latest, err := fetchLatest(ctx, latestURL)
	wg.Wait()
	if err != nil {
		return st
	}
	st.Latest = latest
	st.Available = newer(latest, current)
	// Only once a release meets it: a minimum raised before its release
	// is built would ask for a version that isn't there yet.
	st.Required = st.Available && newer(minimum, current) && !newer(minimum, latest)
	if st.Available {
		st.Problem = inst.problem()
		st.CanInstall = st.Problem == ""
	}
	return st
}

// Install downloads the newest release, checks it, installs it over this
// copy and restarts SpyDB, sending Progress events on the way. It returns
// once the app is about to quit, or with what went wrong.
func (s *Service) Install(ctx context.Context) error {
	if !s.installing.TryLock() {
		return errors.New("SpyDB is already installing the update.")
	}
	defer s.installing.Unlock()
	inst, current := installed()
	st := s.check(ctx, inst, current)
	switch {
	case st.Latest == "":
		return errors.New("Couldn't reach GitHub to find the newest version. Check your internet, then try again.")
	case !st.Available:
		return fmt.Errorf("SpyDB %s is the newest version already.", current)
	case !st.CanInstall:
		return errors.New(st.Problem)
	}

	app := application.Get()
	emit := func(p Progress) {
		if app != nil {
			app.Event.Emit(progressEvent, p)
		}
	}
	_, _, downloadURL := s.urls()
	leftover, err := inst.install(ctx, downloadURL, st.Latest, emit)
	if err != nil {
		return err
	}
	emit(Progress{Step: "restart", Percent: 100})
	if err := inst.relaunch(leftover); err != nil {
		return fmt.Errorf("SpyDB %s is installed, but couldn't restart by itself: quit SpyDB and open it again.", st.Latest)
	}
	if app != nil {
		// After this call has answered the window.
		time.AfterFunc(300*time.Millisecond, app.Quit)
	}
	return nil
}

// Quit closes SpyDB, for a required update the user won't install now.
func (s *Service) Quit() {
	if app := application.Get(); app != nil {
		app.Quit()
	}
}

// fetchLatest reads the newest release's version from where GitHub's
// latest-release page redirects (…/releases/tag/v1.2.3), without
// following it: the web page has no rate limit, unlike GitHub's API.
func fetchLatest(ctx context.Context, url string) (string, error) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	location := resp.Header.Get("Location")
	_, version, ok := strings.Cut(location, "/releases/tag/v")
	if !ok || !validVersion(version) {
		return "", fmt.Errorf("no release version in %q (HTTP %d)", location, resp.StatusCode)
	}
	return version, nil
}

// fetchMinimum reads updates.json: the lowest version allowed on each
// platform, e.g. {"mac": "0.1.8"}. Unreadable, it requires nothing.
func fetchMinimum(ctx context.Context, url string) map[string]string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var policy struct {
		Minimum map[string]string `json:"minimum"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&policy) != nil {
		return nil
	}
	return policy.Minimum
}

// stallTimeout ends a download that has received nothing for this long.
var stallTimeout = 30 * time.Second

// download saves url to path, reporting each whole percent. It stops if
// ctx ends (Cancel in the window) or nothing arrives for stallTimeout.
func download(ctx context.Context, url, path string, progress func(Progress)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	watchdog := time.AfterFunc(stallTimeout, func() { stalled.Store(true); cancel() })
	defer watchdog.Stop()
	fail := func(err error) error {
		if stalled.Load() {
			err = fmt.Errorf("nothing arrived for %v", stallTimeout)
		}
		return fmt.Errorf("Couldn't download the update (%v). Check your internet, then try again.", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fail(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var done int64
	last := -1
	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(stallTimeout)
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if resp.ContentLength > 0 {
				if pct := int(done * 100 / resp.ContentLength); pct != last {
					last = pct
					progress(Progress{Step: "download", Percent: pct})
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fail(readErr)
		}
	}
	if resp.ContentLength > 0 && done != resp.ContentLength {
		return fail(fmt.Errorf("got %d of %d bytes", done, resp.ContentLength))
	}
	return f.Close()
}

var versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

func validVersion(v string) bool { return versionPattern.MatchString(v) }

// newer says whether version a is above b, number by number (0.1.10 is
// above 0.1.9). A version that isn't one is above nothing.
func newer(a, b string) bool {
	if !validVersion(a) || !validVersion(b) {
		return false
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
