package cliapp

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// remoteBackend speaks the server's markdown wire API: every mutation is a
// GET /api/board, an in-memory edit, and a PUT /api/board round-trip. The
// wire format carries no task ids, so tasks are addressed by ephemeral
// listing indexes ("i1", "i2", ...) over the canonical listing order (todo,
// doing, done; document order within a column) — valid only against the
// board as currently listed.
type remoteBackend struct {
	base, token, user string
	client            *http.Client
}

func newRemote(base, token, user string) backend {
	return &remoteBackend{base: base, token: token, user: user, client: &http.Client{Timeout: 30 * time.Second}}
}

func (r *remoteBackend) close() error { return nil }

func (r *remoteBackend) list(status board.Status) ([]item, error) {
	b, err := r.fetchBoard()
	if err != nil {
		return nil, err
	}
	items := r.items(b)
	if status == "" {
		return items, nil
	}
	out := make([]item, 0, len(items))
	for _, it := range items {
		if it.task.Status == status {
			out = append(out, it)
		}
	}
	return out, nil
}

func (r *remoteBackend) add(t board.Task) (item, error) {
	b, err := r.fetchBoard()
	if err != nil {
		return item{}, err
	}
	// Mirror the store defaults so both modes behave alike.
	if t.Status == "" {
		t.Status = board.StatusTodo
	}
	if t.Prio < 1 || t.Prio > 4 {
		t.Prio = 3
	}
	b.Tasks = append(b.Tasks, t)
	renumber(b.Tasks)
	if err := r.putBoard(b); err != nil {
		return item{}, err
	}
	return itemAt(b, len(b.Tasks)-1), nil
}

func (r *remoteBackend) update(ref string, p store.TaskPatch, moveTo *board.Status) (item, error) {
	b, err := r.fetchBoard()
	if err != nil {
		return item{}, err
	}
	ti, _, err := resolveRef(b, ref)
	if err != nil {
		return item{}, err
	}
	applyPatch(&b.Tasks[ti], p)
	if moveTo != nil {
		b.Tasks = moveTaskToEnd(b.Tasks, ti, *moveTo)
		ti = len(b.Tasks) - 1
	}
	renumber(b.Tasks)
	if err := r.putBoard(b); err != nil {
		return item{}, err
	}
	return itemAt(b, ti), nil
}

func (r *remoteBackend) move(ref string, to board.Status) (item, error) {
	return r.update(ref, store.TaskPatch{}, &to)
}

func (r *remoteBackend) remove(ref string) (item, error) {
	b, err := r.fetchBoard()
	if err != nil {
		return item{}, err
	}
	ti, norm, err := resolveRef(b, ref)
	if err != nil {
		return item{}, err
	}
	removed := b.Tasks[ti]
	b.Tasks = append(b.Tasks[:ti], b.Tasks[ti+1:]...)
	renumber(b.Tasks)
	if err := r.putBoard(b); err != nil {
		return item{}, err
	}
	return item{ref: norm, task: removed}, nil
}

// --- listing-order bookkeeping ---

var columnRank = map[board.Status]int{board.StatusTodo: 0, board.StatusDoing: 1, board.StatusDone: 2}

// listingOrder returns the indexes of ts permuted into listing order:
// status column order, then position within the column.
func listingOrder(ts []board.Task) []int {
	idx := make([]int, len(ts))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ta, tb := ts[idx[a]], ts[idx[b]]
		if columnRank[ta.Status] != columnRank[tb.Status] {
			return columnRank[ta.Status] < columnRank[tb.Status]
		}
		return ta.Position < tb.Position
	})
	return idx
}

// renumber recomputes Position per column from slice order, exactly as
// board.Parse does, keeping listing order consistent with what Serialize
// will write.
func renumber(ts []board.Task) {
	pos := map[board.Status]int{}
	for i := range ts {
		ts[i].Position = pos[ts[i].Status]
		pos[ts[i].Status]++
	}
}

// items pairs every task with its ephemeral listing id.
func (r *remoteBackend) items(b board.Board) []item {
	perm := listingOrder(b.Tasks)
	out := make([]item, 0, len(perm))
	for j, ti := range perm {
		out = append(out, item{ref: "i" + strconv.Itoa(j+1), task: b.Tasks[ti]})
	}
	return out
}

// itemAt returns the item for b.Tasks[ti] with its current listing id.
func itemAt(b board.Board, ti int) item {
	for j, idx := range listingOrder(b.Tasks) {
		if idx == ti {
			return item{ref: "i" + strconv.Itoa(j+1), task: b.Tasks[ti]}
		}
	}
	return item{ref: "?", task: b.Tasks[ti]} // unreachable: perm covers all indexes
}

// resolveRef maps an ephemeral id ("i3"; a bare "3" is also accepted) to a
// task index in b.Tasks, returning the normalized id alongside.
func resolveRef(b board.Board, ref string) (ti int, norm string, err error) {
	n, aerr := strconv.Atoi(strings.TrimPrefix(ref, "i"))
	if aerr != nil || n < 1 {
		return 0, "", fmt.Errorf("invalid remote task id %q (remote ids are listing indexes: i1, i2, ...)", ref)
	}
	perm := listingOrder(b.Tasks)
	if n > len(perm) {
		return 0, "", fmt.Errorf("no task i%d (the board has %d tasks)", n, len(perm))
	}
	return perm[n-1], "i" + strconv.Itoa(n), nil
}

// applyPatch mirrors store.UpdateTask's patch semantics in memory.
func applyPatch(t *board.Task, p store.TaskPatch) {
	if p.Emoji != nil {
		t.Emoji = *p.Emoji
	}
	if p.Title != nil {
		t.Title = *p.Title
	}
	if p.Desc != nil {
		t.Desc = *p.Desc
	}
	if p.Due != nil {
		t.Due = *p.Due
	}
	if p.Effort != nil {
		t.Effort = *p.Effort
	}
	if p.Prio != nil {
		t.Prio = *p.Prio
	}
	if p.Tags != nil {
		t.Tags = *p.Tags
	}
	if p.Checks != nil {
		t.Checks = *p.Checks
	}
}

// moveTaskToEnd reassigns ts[ti] to status to and moves it to the end of
// the slice, so it serializes (and therefore lists) at the bottom of its
// new column.
func moveTaskToEnd(ts []board.Task, ti int, to board.Status) []board.Task {
	t := ts[ti]
	t.Status = to
	ts = append(ts[:ti], ts[ti+1:]...)
	return append(ts, t)
}

// --- HTTP wire ---

const maxWireBody = 4 << 20 // generous read cap; the server itself caps PUTs at 1 MiB

func (r *remoteBackend) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, r.base+path, body)
	if err != nil {
		return nil, err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	req.Header.Set("X-KB-User", r.user)
	return req, nil
}

// fetchBoard GETs and parses the wire markdown; a 404 (no board saved yet)
// is an empty board, not an error.
func (r *remoteBackend) fetchBoard() (board.Board, error) {
	req, err := r.newRequest(http.MethodGet, "/api/board", nil)
	if err != nil {
		return board.Board{}, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return board.Board{}, fmt.Errorf("GET %s/api/board: %w", r.base, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return board.Board{Title: "Board"}, nil
	case resp.StatusCode != http.StatusOK:
		return board.Board{}, httpError("GET /api/board", resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWireBody))
	if err != nil {
		return board.Board{}, fmt.Errorf("read board: %w", err)
	}
	return board.Parse(string(data)), nil
}

// putBoard serializes and PUTs the whole board back.
func (r *remoteBackend) putBoard(b board.Board) error {
	req, err := r.newRequest(http.MethodPut, "/api/board", strings.NewReader(board.Serialize(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/markdown; charset=utf-8")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s/api/board: %w", r.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return httpError("PUT /api/board", resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// httpError summarizes a non-success response, including a short body
// snippet when the server sent one.
func httpError(op string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	if msg := strings.TrimSpace(string(snippet)); msg != "" {
		return fmt.Errorf("%s: server returned %s: %s", op, resp.Status, msg)
	}
	return fmt.Errorf("%s: server returned %s", op, resp.Status)
}
