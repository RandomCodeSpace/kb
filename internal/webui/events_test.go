package webui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// eventFixture is a live server plus the hub and store behind it, so a test
// can write to the board the way the CLI does (straight to the store, no
// nudge) as well as through the API.
type eventFixture struct {
	srv *httptest.Server
	hub *eventHub
	st  *store.Store
}

// newEventFixture serves a fresh board over TCP. Sampling is fast and pings
// are off unless a test asks for them, so nothing waits on wall-clock defaults.
func newEventFixture(t *testing.T, tune func(*eventHub)) *eventFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := cliapp.OpenLocalStore(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	handler, hub := newHandler(st, "default", dir, "v-test", false)
	hub.sample = 10 * time.Millisecond
	hub.ping = time.Hour
	if tune != nil {
		tune(hub)
	}
	srv := httptest.NewServer(handler)
	// The hub closes first: srv.Close waits for outstanding requests, and an
	// event stream is outstanding until its hub ends it.
	t.Cleanup(func() {
		hub.Close()
		srv.Close()
		st.Close()
	})
	return &eventFixture{srv: srv, hub: hub, st: st}
}

// stream opens /api/events and reads it with a scanner in the background,
// handing whole frames (the lines between blank lines) to the test.
func (f *eventFixture) stream(t *testing.T, headers ...string) (*http.Response, <-chan string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	frames := make(chan string, 64)
	go func() {
		defer close(frames)
		defer res.Body.Close()
		scanner := bufio.NewScanner(res.Body)
		var frame []string
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				frame = append(frame, line)
				continue
			}
			if len(frame) > 0 {
				frames <- strings.Join(frame, "\n")
				frame = nil
			}
		}
	}()
	return res, frames, cancel
}

// post writes through the API, the way the browser does.
func (f *eventFixture) post(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.srv.Client().Post(f.srv.URL+path, jsonContentType, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// nextFrame takes the next frame or fails the test.
func nextFrame(t *testing.T, frames <-chan string) string {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("event stream ended early")
		}
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return ""
}

// parseFrame splits one frame into its id, event name and decoded payload.
func parseFrame(t *testing.T, frame string) (name string, id int64, data map[string]any) {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		value, ok := strings.CutPrefix(line, "id: ")
		if ok {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				t.Fatalf("id line %q: %v", line, err)
			}
			id = parsed
			continue
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			name = value
			continue
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			if err := json.Unmarshal([]byte(value), &data); err != nil {
				t.Fatalf("data line %q: %v", line, err)
			}
		}
	}
	return name, id, data
}

// wantEvent takes the next frame and checks its name, returning its revision.
func wantEvent(t *testing.T, frames <-chan string, want string) int64 {
	t.Helper()
	frame := nextFrame(t, frames)
	name, id, data := parseFrame(t, frame)
	if name != want {
		t.Fatalf("event = %q, want %q (frame %q)", name, want, frame)
	}
	revision, _ := data["revision"].(float64)
	if int64(revision) != id {
		t.Fatalf("id %d does not match revision %v in %q", id, data["revision"], frame)
	}
	return id
}

// wantHello reads the opening retry directive and hello frame.
func wantHello(t *testing.T, frames <-chan string) int64 {
	t.Helper()
	if first := nextFrame(t, frames); first != "retry: 2000" {
		t.Fatalf("first frame = %q, want the retry directive", first)
	}
	return wantEvent(t, frames, "hello")
}

func TestEventStreamAnnouncesItselfAndPushesChanges(t *testing.T) {
	f := newEventFixture(t, nil)
	res, frames, _ := f.stream(t)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	for header, want := range map[string]string{
		contentType:         "text/event-stream",
		"Cache-Control":     "no-store",
		"X-Accel-Buffering": "no",
	} {
		if got := res.Header.Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	if first := nextFrame(t, frames); first != "retry: 2000" {
		t.Fatalf("first frame = %q, want the retry directive", first)
	}
	name, id, data := parseFrame(t, nextFrame(t, frames))
	if name != "hello" {
		t.Fatalf("first event = %q, want hello", name)
	}
	if data["version"] != "v-test" {
		t.Fatalf("hello version = %v, want v-test", data["version"])
	}
	hello := id

	if got := f.post(t, "/api/tasks", map[string]any{"title": "pushed", "project": "work"}).StatusCode; got != http.StatusCreated {
		t.Fatalf("POST /api/tasks = %d, want 201", got)
	}
	if revision := wantEvent(t, frames, "change"); revision <= hello {
		t.Fatalf("change revision = %d, want more than the hello revision %d", revision, hello)
	}
}

// A write that never touches the API — the CLI, the TUI, another kb process —
// reaches the browser through the data-version sampler alone.
func TestEventStreamPushesWritesMadeOutsideTheAPI(t *testing.T) {
	f := newEventFixture(t, nil)
	_, frames, _ := f.stream(t)
	hello := wantHello(t, frames)
	if _, err := f.st.AddTask("default", board.Task{Title: "added by the CLI"}); err != nil {
		t.Fatal(err)
	}
	if revision := wantEvent(t, frames, "change"); revision <= hello {
		t.Fatalf("change revision = %d, want more than %d", revision, hello)
	}
}

func TestEventStreamPingsIdleConnections(t *testing.T) {
	f := newEventFixture(t, func(h *eventHub) { h.ping = 10 * time.Millisecond })
	_, frames, _ := f.stream(t)
	wantHello(t, frames)
	if frame := nextFrame(t, frames); frame != ": ping" {
		t.Fatalf("idle frame = %q, want a ping comment", frame)
	}
}

// A browser that reconnects replays the last id it saw; anything older than
// the board's revision means it missed a change while it was gone.
func TestEventStreamReplaysAChangeMissedWhileDisconnected(t *testing.T) {
	f := newEventFixture(t, func(h *eventHub) { h.ping = 10 * time.Millisecond })
	if _, err := f.st.AddTask("default", board.Task{Title: "written while away"}); err != nil {
		t.Fatal(err)
	}
	_, frames, _ := f.stream(t, "Last-Event-ID", "0")
	hello := wantHello(t, frames)
	if replay := wantEvent(t, frames, "change"); replay != hello {
		t.Fatalf("replayed revision = %d, want the current %d", replay, hello)
	}

	// Up to date, or an id that is not a number at all: no replay, so the next
	// frame is the idle ping.
	for _, id := range []string{strconv.FormatInt(hello, 10), "not-a-revision"} {
		_, current, _ := f.stream(t, "Last-Event-ID", id)
		wantHello(t, current)
		if frame := nextFrame(t, current); frame != ": ping" {
			t.Fatalf("Last-Event-ID %q: next frame = %q, want a ping", id, frame)
		}
	}
}

func TestEventStreamCapsConcurrentConnections(t *testing.T) {
	f := newEventFixture(t, func(h *eventHub) { h.max = 1 })
	_, frames, _ := f.stream(t)
	wantHello(t, frames)

	res, err := f.srv.Client().Get(f.srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second stream = %d, want 503", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "too many open event streams") {
		t.Fatalf("body = %q, want the cap message", body)
	}
}

func TestEventStreamEndsOnShutdown(t *testing.T) {
	f := newEventFixture(t, nil)
	_, frames, _ := f.stream(t)
	wantHello(t, frames)
	f.hub.Close()
	f.hub.Close() // idempotent
	select {
	case _, ok := <-frames:
		if ok {
			t.Fatal("shutdown emitted an event instead of ending the stream")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not end the stream")
	}
	if _, _, ok := f.hub.subscribe(); ok {
		t.Fatal("a closed hub accepted a subscriber")
	}
}

func TestEventStreamEndsOnClientDisconnect(t *testing.T) {
	f := newEventFixture(t, nil)
	_, frames, cancel := f.stream(t)
	wantHello(t, frames)
	cancel()
	// The handler's cleanup drops the subscriber, which stops the sampler.
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.hub.mu.Lock()
		subs, stopped := len(f.hub.subs), f.hub.stop == nil
		f.hub.mu.Unlock()
		if subs == 0 && stopped {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after disconnect: %d subscribers, sampler stopped %v", subs, stopped)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// unflushableWriter is an http.ResponseWriter that cannot stream.
type unflushableWriter struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (w *unflushableWriter) Header() http.Header         { return w.header }
func (w *unflushableWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *unflushableWriter) WriteHeader(code int)        { w.code = code }

func TestEventStreamRefusesAResponseItCannotFlush(t *testing.T) {
	w := &unflushableWriter{header: http.Header{}}
	(&server{}).streamEvents(w, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	if w.code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.code)
	}
	if !strings.Contains(w.body.String(), "cannot stream") {
		t.Fatalf("body = %q, want the streaming refusal", w.body.String())
	}
}

// newHubFixture builds a hub over a bare store, for the sampler's own
// behaviour rather than the wire format.
func newHubFixture(t *testing.T, dataDir string) (*eventHub, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := cliapp.OpenLocalStore(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := newEventHub(st, "default", dataDir)
	hub.sample = 10 * time.Millisecond
	t.Cleanup(hub.Close)
	return hub, st
}

// wantRevision waits for the sampler to report a change.
func wantRevision(t *testing.T, sub <-chan int64) int64 {
	t.Helper()
	select {
	case revision := <-sub:
		return revision
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler reported no change")
	}
	return 0
}

// Without a data-version watcher the sampler reads the revision every tick:
// slower, but a change still lands.
func TestEventHubSamplesWithoutADataVersionWatcher(t *testing.T) {
	hub, st := newHubFixture(t, "")
	sub, _, ok := hub.subscribe()
	if !ok {
		t.Fatal("subscribe refused")
	}
	defer hub.unsubscribe(sub)
	if _, err := st.AddTask("default", board.Task{Title: "sampled"}); err != nil {
		t.Fatal(err)
	}
	if revision := wantRevision(t, sub); revision == 0 {
		t.Fatal("revision = 0, want the board's revision")
	}
}

// failingWatcher stands in for a data-version connection that answers nothing
// useful; the gate must stay open rather than freeze the board.
type failingWatcher struct{}

func (failingWatcher) DataVersion(context.Context) (int64, error) {
	return 0, errors.New("no data_version here")
}
func (failingWatcher) Close() error { return nil }

func TestEventHubToleratesAnUnusableWatcher(t *testing.T) {
	for name, open := range map[string]func(context.Context, string) (dataVersionWatcher, error){
		"cannot open": func(context.Context, string) (dataVersionWatcher, error) {
			return nil, errors.New("cannot open the database")
		},
		"cannot read": func(context.Context, string) (dataVersionWatcher, error) {
			return failingWatcher{}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			original := openDataVersionWatcher
			openDataVersionWatcher = open
			t.Cleanup(func() { openDataVersionWatcher = original })

			hub, st := newHubFixture(t, t.TempDir())
			sub, _, ok := hub.subscribe()
			if !ok {
				t.Fatal("subscribe refused")
			}
			defer hub.unsubscribe(sub)
			if _, err := st.AddTask("default", board.Task{Title: "sampled anyway"}); err != nil {
				t.Fatal(err)
			}
			if revision := wantRevision(t, sub); revision == 0 {
				t.Fatal("revision = 0, want the board's revision")
			}
		})
	}
}

// A subscriber that has not read its last revision gets the newest one; the
// hub never blocks on it.
func TestEventHubKeepsOnlyTheNewestRevisionForASlowSubscriber(t *testing.T) {
	hub, _ := newHubFixture(t, "")
	sub, _, ok := hub.subscribe()
	if !ok {
		t.Fatal("subscribe refused")
	}
	defer hub.unsubscribe(sub)
	hub.broadcast(7)
	hub.broadcast(9)
	hub.broadcast(9) // not newer: dropped
	if revision := wantRevision(t, sub); revision != 9 {
		t.Fatalf("revision = %d, want 9", revision)
	}
}

func TestEventHubReadsZeroFromAClosedStore(t *testing.T) {
	hub, st := newHubFixture(t, "")
	st.Close()
	if revision := hub.readRevision(); revision != 0 {
		t.Fatalf("revision = %d, want 0 from a closed store", revision)
	}
}
