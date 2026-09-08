package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui"
)

func init() { featureRoutes = append(featureRoutes, (*server).eventRoutes) }

const (
	// eventRetryMillis is the reconnect delay browsers apply after a dropped
	// stream. Two seconds matches the poll it replaces.
	eventRetryMillis = 2000
	// eventSampleInterval is how often the sampler looks for outside writes
	// (another kb process, the TUI, an editor). It is one PRAGMA per tick.
	eventSampleInterval = 400 * time.Millisecond
	// eventPingInterval keeps proxies and phone radios from reclaiming an
	// idle connection.
	eventPingInterval = 15 * time.Second
	// maxEventStreams caps concurrent subscribers. A board is a single-user
	// local tool; anything past this is a runaway client, not a user.
	maxEventStreams = 64
)

// dataVersionWatcher is the slice of tui.DataVersionWatcher the sampler uses.
type dataVersionWatcher interface {
	DataVersion(context.Context) (int64, error)
	Close() error
}

// openDataVersionWatcher is the TUI's cross-process change signal, in a
// variable so tests can fail it.
var openDataVersionWatcher = func(ctx context.Context, path string) (dataVersionWatcher, error) {
	return tui.OpenDataVersionWatcher(ctx, path)
}

// eventHub fans board revisions out to the /api/events subscribers.
//
// One sampler goroutine runs while at least one browser is connected. It gates
// on SQLite's PRAGMA data_version — the same signal internal/tui watches, read
// on its own pinned connection so a write by any process moves it — and reads
// store.ReadBoardSnapshot only when that moves. Mutating API requests nudge it
// directly so a browser never waits a tick for its own write.
type eventHub struct {
	st     *store.Store
	user   string
	dbPath string
	sample time.Duration
	ping   time.Duration
	max    int

	// quit is closed once, by Close, to end every live stream.
	quit chan struct{}
	// wake carries nudges from mutating requests; capacity 1, because a
	// nudge already queued says the same thing.
	wake chan struct{}

	mu     sync.Mutex
	subs   map[chan int64]struct{}
	rev    int64
	stop   chan struct{} // sampler lifetime; nil when nobody is subscribed
	closed bool
}

// newEventHub builds the hub for one server. An empty dataDir (the CLI's "use
// the default location") leaves dbPath empty: the sampler then reads the
// revision every tick rather than inventing a path and creating a stray
// database beside the working directory.
func newEventHub(st *store.Store, user, dataDir string) *eventHub {
	dbPath := ""
	if dataDir != "" {
		dbPath = filepath.Join(dataDir, "kb.db")
	}
	return &eventHub{
		st:     st,
		user:   user,
		dbPath: dbPath,
		sample: eventSampleInterval,
		ping:   eventPingInterval,
		max:    maxEventStreams,
		quit:   make(chan struct{}),
		wake:   make(chan struct{}, 1),
		subs:   map[chan int64]struct{}{},
	}
}

// Notify asks the sampler to read the revision now. It never blocks: a nudge
// already queued covers this one.
func (h *eventHub) Notify() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Close ends every live stream and refuses new ones. Idempotent.
func (h *eventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	close(h.quit)
}

// readRevision reads the board's monotonic revision. A failing store reads as
// zero, which broadcast treats as "nothing new" rather than a spurious change.
func (h *eventHub) readRevision() int64 {
	snapshot, err := h.st.ReadBoardSnapshot(h.user)
	if err != nil {
		return 0
	}
	return snapshot.Revision
}

// subscribe registers a stream and reports the revision its hello carries. It
// returns false when the hub is closed or the cap is reached.
func (h *eventHub) subscribe() (chan int64, int64, bool) {
	rev := h.readRevision()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.subs) >= h.max {
		return nil, 0, false
	}
	if rev > h.rev {
		h.rev = rev
	}
	ch := make(chan int64, 1)
	h.subs[ch] = struct{}{}
	if len(h.subs) == 1 {
		h.stop = make(chan struct{})
		go h.run(h.stop)
	}
	return ch, h.rev, true
}

// unsubscribe drops a stream, and the sampler with the last one.
func (h *eventHub) unsubscribe(ch chan int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
	if len(h.subs) == 0 && h.stop != nil {
		close(h.stop)
		h.stop = nil
	}
}

// broadcast delivers rev to every subscriber. The hub is the only sender on a
// one-slot channel, so a subscriber too slow to have read the previous
// revision loses it instead of stalling the hub: the newest revision is the
// only one worth having, since the client re-reads the board either way.
func (h *eventHub) broadcast(rev int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rev <= h.rev {
		return
	}
	h.rev = rev
	for ch := range h.subs {
		select {
		case <-ch:
		default:
		}
		ch <- rev
	}
}

// run samples until the last subscriber leaves or the hub closes.
func (h *eventHub) run(stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed, closeWatcher := h.watchDataVersion(ctx)
	defer closeWatcher()
	ticker := time.NewTicker(h.sample)
	defer ticker.Stop()
	for {
		select {
		case <-h.quit:
			return
		case <-stop:
			return
		case <-h.wake:
		case <-ticker.C:
			if !changed() {
				continue
			}
		}
		h.broadcast(h.readRevision())
	}
}

// watchDataVersion returns the cheap "the database may have moved" test and
// its closer. Without a usable watcher the gate stays open and the sampler
// reads the revision every tick — more work, still correct.
func (h *eventHub) watchDataVersion(ctx context.Context) (changed func() bool, closeWatcher func()) {
	always := func() bool { return true }
	if h.dbPath == "" {
		return always, func() {}
	}
	w, err := openDataVersionWatcher(ctx, h.dbPath)
	if err != nil {
		return always, func() {}
	}
	var last int64
	seen := false
	return func() bool {
			version, err := w.DataVersion(ctx)
			if err != nil {
				return true
			}
			moved := !seen || version != last
			last, seen = version, true
			return moved
		}, func() {
			_ = w.Close()
		}
}

// notifyChanges nudges the hub after every mutating API request, so a browser
// sees its own write without waiting for the sampler. A nudge that finds the
// revision unchanged broadcasts nothing, so nudging a refused write is free.
func (s *server) notifyChanges(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if isMutating(r.Method) {
			s.events.Notify()
		}
	})
}

func (s *server) eventRoutes() []route {
	return []route{{"GET", "/api/events", s.streamEvents}}
}

// helloEvent opens a stream: the revision the board is on, and the build
// identifier, so a client reconnecting into a restarted kb can tell.
type helloEvent struct {
	Revision int64  `json:"revision"`
	Version  string `json:"version"`
}

// changeEvent says only that the board moved; the client re-reads /api/tasks
// with its ETag.
type changeEvent struct {
	Revision int64 `json:"revision"`
}

// streamEvents is GET /api/events: text/event-stream, one `change` per board
// revision, `: ping` comments to hold the connection open. See the change
// detection section of docs/web-api.md.
func (s *server) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "this server cannot stream events")
		return
	}
	sub, rev, ok := s.events.subscribe()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "too many open event streams")
		return
	}
	defer s.events.unsubscribe(sub)

	head := w.Header()
	head.Set(contentType, "text/event-stream")
	head.Set("Cache-Control", "no-store")
	// Tell an intermediary (nginx and friends) not to buffer the body; without
	// it a proxied stream arrives in silent blocks.
	head.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: %d\n\n", eventRetryMillis)
	writeEvent(w, "hello", rev, helloEvent{Revision: rev, Version: s.version})
	// A reconnecting browser replays the id it last saw. Anything older than
	// the current revision means it missed a change while it was away.
	if last, ok := parseEventID(r.Header.Get("Last-Event-ID")); ok && last < rev {
		writeEvent(w, "change", rev, changeEvent{Revision: rev})
	}
	flusher.Flush()

	ping := time.NewTicker(s.events.ping)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.events.quit:
			return
		case rev := <-sub:
			writeEvent(w, "change", rev, changeEvent{Revision: rev})
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}

// writeEvent emits one SSE frame. The revision doubles as the event id, which
// is what makes Last-Event-ID replay work.
func writeEvent(w http.ResponseWriter, name string, id int64, payload any) {
	// The payloads are fixed structs of scalars; marshalling cannot fail.
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, name, data)
}

// parseEventID reads a Last-Event-ID header. A missing or malformed value is
// simply not a replay request.
func parseEventID(header string) (int64, bool) {
	if header == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(header, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
