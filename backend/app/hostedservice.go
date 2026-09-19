package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"tonelab/backend/version"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/hosted"
	"tonelab/backend/search"
)

// HostedStatus is the account block on the settings screen. Nothing here
// is a secret: the key stays in the config file, and the screen learns
// only that it is signed in.
type HostedStatus struct {
	URL      string
	SignedIn bool
	Email    string
	Plan     string
	Active   bool
	Reason   string
	Month    hosted.Window
	Day      hosted.Window
	Searches hosted.Window
	Models   []string
	// Error is set when the service could not be read, with the sentence
	// to show; SignedIn still says whether a key is on file.
	Error string
}

// SignInState is what the screen polls while the browser leg runs.
type SignInState struct {
	Running bool
	Done    bool
	// UserCode and VerifyURL are shown to the person: the browser opens
	// on VerifyURL, and the code is what they check there.
	UserCode  string
	VerifyURL string
	Error     string
}

// Update is what the window shows about versions: this one, the latest
// the service knows, and whether they differ. Available is false for a
// dev build, which has no tag to be behind.
type Update struct {
	Current   string
	Latest    string
	Available bool
	// Error is set when the service could not be asked; the window says
	// nothing rather than "up to date".
	Error string
}

// HostedService is "Sign in with Tonelab": one device flow at a time,
// run in the background so the window stays live while the browser
// does the rest, and a status read for the settings screen.
type HostedService struct {
	path     string
	agent    *agent.Orchestrator
	previews *agent.Orchestrator
	apply    func(search.Provider)
	open     func(url string) error

	mu     sync.Mutex
	state  SignInState
	cancel context.CancelFunc
}

func NewHostedService(path string, live, previews *agent.Orchestrator, applySearch func(search.Provider), open func(string) error) *HostedService {
	return &HostedService{path: path, agent: live, previews: previews, apply: applySearch, open: open}
}

// OpenSite opens a page of the site in the browser: the plans, the
// downloads. Only a path is taken, so the window cannot be made to open
// somewhere else.
func (h *HostedService) OpenSite(path string) error {
	if h.open == nil {
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return h.open(config.DefaultSiteURL + path)
}

// CheckUpdate compares this build with the latest release the service
// reports. A dev build is never behind; a service that cannot be reached
// is reported, not guessed about.
func (h *HostedService) CheckUpdate() (Update, error) {
	update := Update{Current: version.Version}
	if version.Version == "dev" {
		return update, nil
	}
	settings, err := config.Load(h.path)
	url := config.DefaultHostedURL
	if err == nil {
		url = settings.HostedURL()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release, err := hosted.NewClient(url, "").Latest(ctx)
	if err != nil {
		update.Error = explain(err)
		return update, nil
	}
	update.Latest = release.Tag
	update.Available = newer(release.Tag, version.Version)
	return update, nil
}

// newer says whether tag is a later release than current, comparing the
// numbers in order. Anything unparseable is not newer: a wrong "update
// available" nags forever, a missed one costs a day.
func newer(tag, current string) bool {
	a, okA := parts(tag)
	b, okB := parts(current)
	if !okA || !okB {
		return false
	}
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func parts(tag string) ([3]int, bool) {
	var out [3]int
	tag = strings.TrimPrefix(tag, "v")
	// A dirty or untagged build carries a suffix; only the numbers count.
	if i := strings.IndexAny(tag, "-+"); i >= 0 {
		tag = tag[:i]
	}
	fields := strings.Split(tag, ".")
	if len(fields) != 3 {
		return out, false
	}
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Status reads the account when signed in. A service that does not
// answer is reported, not failed on: the screen still shows the rest.
func (h *HostedService) Status() (HostedStatus, error) {
	settings, err := config.Load(h.path)
	if err != nil {
		return HostedStatus{URL: config.DefaultHostedURL}, nil
	}
	status := HostedStatus{URL: settings.HostedURL(), SignedIn: settings.Hosted.APIKey != ""}
	if !status.SignedIn {
		return status, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	account, err := hosted.NewClient(settings.HostedURL(), settings.DeviceID).Account(ctx, settings.Hosted.APIKey)
	if err != nil {
		status.Error = explain(err)
		return status, nil
	}
	status.Email, status.Plan, status.Active, status.Reason = account.Email, account.Plan, account.Active, account.Reason
	status.Month, status.Day, status.Searches, status.Models = account.Month, account.Day, account.Searches, account.Models
	return status, nil
}

// SignIn starts the device flow against url (empty for the default) and
// returns at once with the code to show. Approval is awaited in the
// background; State reports how it went.
func (h *HostedService) SignIn(url string) (SignInState, error) {
	h.mu.Lock()
	if h.state.Running {
		state := h.state
		h.mu.Unlock()
		return state, nil
	}
	h.mu.Unlock()

	settings, err := config.Load(h.path)
	if err != nil {
		return SignInState{Error: "Fix the settings file first: " + err.Error()}, nil
	}
	url = strings.TrimSpace(url)
	if url == "" {
		url = settings.HostedURL()
	}
	client := hosted.NewClient(url, settings.DeviceID)
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Minute)
	started, err := client.Start(ctx, deviceName())
	if err != nil {
		cancel()
		return SignInState{Error: explain(err)}, nil
	}
	h.mu.Lock()
	h.state = SignInState{Running: true, UserCode: started.UserCode, VerifyURL: started.VerifyURLComplete}
	h.cancel = cancel
	state := h.state
	h.mu.Unlock()

	if h.open != nil {
		// Best effort: a browser that will not open leaves the person the
		// URL and the code on screen, which is enough.
		_ = h.open(started.VerifyURLComplete)
	}
	go h.await(ctx, cancel, client, started, url)
	return state, nil
}

func (h *HostedService) await(ctx context.Context, cancel context.CancelFunc, client *hosted.Client, started hosted.Started, url string) {
	defer cancel()
	key, err := client.Wait(ctx, started)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state.Running = false
	h.cancel = nil
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.state = SignInState{}
			return
		}
		h.state.Error = explain(err)
		return
	}
	if err := h.adopt(url, key); err != nil {
		h.state.Error = "Signed in, but the settings could not be saved: " + err.Error()
		return
	}
	h.state.Done = true
}

// adopt points the model and search at the subscription. The person's
// own endpoint is what they had before, and the LLM section still shows
// what is in use, so editing it back is one field.
func (h *HostedService) adopt(url, key string) error {
	settings, err := config.Load(h.path)
	if err != nil {
		return err
	}
	origin := strings.TrimRight(url, "/")
	settings.Hosted = config.Hosted{URL: origin, APIKey: key}
	settings.LLM = config.LLM{BaseURL: hosted.Base(origin), APIKey: key, Model: "tonelab"}
	settings.Search = config.Search{Provider: "tonelab", APIKey: key, BaseURL: hosted.Base(origin)}
	if err := config.Save(h.path, settings); err != nil {
		return err
	}
	h.applySettings(settings)
	return nil
}

func (h *HostedService) applySettings(settings config.Config) {
	baseURL, apiKey, model, device := settings.AgentConfig()
	llm := agent.Config{BaseURL: baseURL, APIKey: apiKey, Model: model, DeviceID: device}
	h.agent.Reconfigure(llm)
	h.previews.Reconfigure(llm)
	if h.apply != nil {
		provider, _ := search.New(settings.SearchConfig())
		h.apply(provider)
	}
}

// State is polled by the screen while a sign-in runs.
func (h *HostedService) State() (SignInState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state, nil
}

// Cancel stops a sign-in that is waiting for approval.
func (h *HostedService) Cancel() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
	}
	return nil
}

// SignOut revokes this device's key at the service and forgets it. The
// LLM and search sections are cleared of the key too, since a dead key
// left there would fail every turn with a sentence about signing in.
func (h *HostedService) SignOut() (HostedStatus, error) {
	settings, err := config.Load(h.path)
	if err != nil {
		return HostedStatus{}, nil
	}
	if settings.Hosted.APIKey != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := hosted.NewClient(settings.HostedURL(), settings.DeviceID)
		if account, err := client.Account(ctx, settings.Hosted.APIKey); err == nil {
			// Revocation is best effort: an unreachable service still lets
			// the person sign out here, and the key dies at the next reset
			// or from the account page.
			_ = client.SignOut(ctx, settings.Hosted.APIKey, account.KeyID)
		}
	}
	hostedURL := settings.HostedURL()
	if settings.SignedIn() {
		settings.LLM = config.LocalLLM()
	}
	if settings.Search.Provider == "tonelab" {
		settings.Search = config.Search{}
	}
	settings.Hosted = config.Hosted{URL: hostedURL}
	if err := config.Save(h.path, settings); err != nil {
		return HostedStatus{}, fmt.Errorf("hosted: %w", err)
	}
	h.applySettings(settings)
	h.mu.Lock()
	h.state = SignInState{}
	h.mu.Unlock()
	return HostedStatus{URL: hostedURL}, nil
}

// deviceName is what the approval page shows, so a person with two
// machines can tell which one is asking.
func deviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = runtime.GOOS
	}
	return strings.TrimSuffix(host, ".local") + ", Tonelab"
}

// explain turns a client error into the sentence the screen shows.
func explain(err error) string {
	var refused *hosted.Error
	switch {
	case errors.As(err, &refused):
		return refused.Message
	case errors.Is(err, hosted.ErrUnreachable):
		return "Could not reach the Tonelab service. Check the URL and your connection."
	case errors.Is(err, context.DeadlineExceeded):
		return "The sign-in took too long. Start again."
	}
	return err.Error()
}
