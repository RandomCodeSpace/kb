package cliapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// remoteBackend speaks the server's per-task JSON API (/api/tasks and
// friends), giving remote mode the same granularity and the same stable #n
// addressing as the local store. The old ephemeral i-N listing indexes are
// gone: they were documented as never-store, and the server now resolves
// stable numbers, UUIDs, and unique prefixes itself.
type remoteBackend struct {
	base, token, user string
	client            *http.Client
}

// newRemote builds the client for `KB_SERVER` mode. Every request carries
// the server token, so a redirect to another host would hand it to that
// host. Redirects therefore cannot change host or downgrade HTTPS to HTTP.
func newRemote(base, token, user string) backend {
	return &remoteBackend{base: base, token: token, user: user, client: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: remoteRedirectPolicy,
	}}
}

func remoteRedirectPolicy(req *http.Request, via []*http.Request) error {
	origin := via[0].URL
	if origin.User != nil || req.URL.User != nil {
		return errors.New("refusing redirect with URL credentials")
	}
	if !isRemoteHTTPURL(origin) || !isRemoteHTTPURL(req.URL) {
		return errors.New("refusing redirect to non-HTTP(S) URL")
	}
	if normalizeRemoteRedirectHost(req.URL.Hostname()) != normalizeRemoteRedirectHost(origin.Hostname()) || req.URL.Port() != origin.Port() {
		return errors.New("refusing cross-host redirect")
	}
	if strings.EqualFold(origin.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http") {
		return errors.New("refusing HTTPS-to-HTTP redirect")
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

func isRemoteHTTPURL(u *url.URL) bool {
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}

func normalizeRemoteRedirectHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

func (r *remoteBackend) close() error { return nil }

// checkRemoteRef rejects the retired i-N indexes with a pointer at their
// replacement. Everything else passes through for the server to resolve.
func checkRemoteRef(ref string) error {
	rest := strings.TrimPrefix(ref, "i")
	if rest == ref || rest == "" {
		return nil
	}
	if _, err := strconv.Atoi(rest); err == nil {
		return fmt.Errorf("ephemeral i-N task ids are gone; use the stable number instead (kb list shows #n)")
	}
	return nil
}

// --- wire shapes (matching the server's /api/tasks API) ---

type wireCheck struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type wireTask struct {
	ID        string      `json:"id"`
	Seq       int         `json:"seq"`
	Emoji     string      `json:"emoji"`
	Title     string      `json:"title"`
	Desc      string      `json:"desc"`
	Status    string      `json:"status"`
	Blocked   bool        `json:"blocked"`
	Prio      int         `json:"prio"`
	Due       string      `json:"due"`
	Effort    string      `json:"effort"`
	Tags      []string    `json:"tags"`
	Checks    []wireCheck `json:"checks"`
	Position  int         `json:"position"`
	CreatedAt string      `json:"createdAt"`
	MovedAt   string      `json:"movedAt"`
}

type wireComment struct {
	ID        int    `json:"id"`
	TaskSeq   int    `json:"task"`
	TaskID    string `json:"taskId"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

type wireTaskDetail struct {
	wireTask
	Blocks    []int         `json:"blocks"`
	BlockedBy []int         `json:"blockedBy"`
	Comments  []wireComment `json:"comments"`
}

type wireWrite struct {
	Title   *string      `json:"title,omitempty"`
	Desc    *string      `json:"desc,omitempty"`
	Emoji   *string      `json:"emoji,omitempty"`
	Status  *string      `json:"status,omitempty"`
	Prio    *int         `json:"prio,omitempty"`
	Due     *string      `json:"due,omitempty"`
	Effort  *string      `json:"effort,omitempty"`
	Blocked *bool        `json:"blocked,omitempty"`
	Tags    *[]string    `json:"tags,omitempty"`
	Checks  *[]wireCheck `json:"checks,omitempty"`
	Force   bool         `json:"force,omitempty"`
}

func fromWireTask(wt wireTask) board.Task {
	t := board.Task{
		ID: wt.ID, Seq: wt.Seq, Emoji: wt.Emoji, Title: wt.Title, Desc: wt.Desc,
		Status: board.Status(wt.Status), Blocked: wt.Blocked, Prio: wt.Prio,
		Due: wt.Due, Effort: wt.Effort, Tags: wt.Tags, Position: wt.Position,
	}
	for _, c := range wt.Checks {
		t.Checks = append(t.Checks, board.Check{Text: c.Text, Done: c.Done})
	}
	if ts, err := time.Parse(time.RFC3339Nano, wt.CreatedAt); err == nil {
		t.CreatedAt = ts
	}
	if ts, err := time.Parse(time.RFC3339Nano, wt.MovedAt); err == nil {
		t.MovedAt = ts
	}
	return t
}

func fromWireComment(wc wireComment) store.Comment {
	c := store.Comment{ID: wc.ID, TaskSeq: wc.TaskSeq, TaskID: wc.TaskID, Author: wc.Author, Body: wc.Body}
	if ts, err := time.Parse(time.RFC3339Nano, wc.CreatedAt); err == nil {
		c.CreatedAt = ts
	}
	return c
}

func taskToWrite(t board.Task) wireWrite {
	w := wireWrite{Title: &t.Title}
	if t.Desc != "" {
		w.Desc = &t.Desc
	}
	if t.Emoji != "" {
		w.Emoji = &t.Emoji
	}
	if t.Status != "" {
		s := string(t.Status)
		w.Status = &s
	}
	if t.Prio != 0 {
		w.Prio = &t.Prio
	}
	if t.Due != "" {
		w.Due = &t.Due
	}
	if t.Effort != "" {
		w.Effort = &t.Effort
	}
	if t.Blocked {
		w.Blocked = &t.Blocked
	}
	if len(t.Tags) > 0 {
		tags := t.Tags
		w.Tags = &tags
	}
	if len(t.Checks) > 0 {
		checks := make([]wireCheck, 0, len(t.Checks))
		for _, c := range t.Checks {
			checks = append(checks, wireCheck{Text: c.Text, Done: c.Done})
		}
		w.Checks = &checks
	}
	return w
}

func patchToWrite(p store.TaskPatch, moveTo *board.Status, force bool) wireWrite {
	w := wireWrite{
		Title: p.Title, Desc: p.Desc, Emoji: p.Emoji,
		Prio: p.Prio, Due: p.Due, Effort: p.Effort, Blocked: p.Blocked,
		Force: force,
	}
	if p.Tags != nil {
		w.Tags = p.Tags
	}
	if p.Checks != nil {
		checks := make([]wireCheck, 0, len(*p.Checks))
		for _, c := range *p.Checks {
			checks = append(checks, wireCheck{Text: c.Text, Done: c.Done})
		}
		w.Checks = &checks
	}
	if moveTo != nil {
		s := string(*moveTo)
		w.Status = &s
	}
	return w
}

// --- backend interface ---

func (r *remoteBackend) list(filter store.TaskFilter) ([]item, error) {
	params := url.Values{}
	if filter.Status != "" {
		params.Set("status", string(filter.Status))
	}
	if filter.Search != "" {
		params.Set("search", filter.Search)
	}
	for _, tag := range filter.Tags {
		params.Add("tag", tag)
	}
	path := "/api/tasks"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var tasks []wireTask
	if err := r.call(http.MethodGet, path, nil, &tasks); err != nil {
		return nil, err
	}
	items := make([]item, 0, len(tasks))
	for _, wt := range tasks {
		t := fromWireTask(wt)
		items = append(items, item{ref: t.ID, task: t})
	}
	return items, nil
}

func (r *remoteBackend) add(t board.Task) (item, error) {
	var created wireTask
	if err := r.call(http.MethodPost, "/api/tasks", taskToWrite(t), &created); err != nil {
		return item{}, err
	}
	out := fromWireTask(created)
	return item{ref: out.ID, task: out}, nil
}

func (r *remoteBackend) update(ref string, p store.TaskPatch, moveTo *board.Status, force bool) (item, error) {
	if err := checkRemoteRef(ref); err != nil {
		return item{}, err
	}
	var updated wireTask
	if err := r.call(http.MethodPatch, "/api/tasks/"+url.PathEscape(ref), patchToWrite(p, moveTo, force), &updated); err != nil {
		return item{}, err
	}
	out := fromWireTask(updated)
	return item{ref: out.ID, task: out}, nil
}

func (r *remoteBackend) move(ref string, to board.Status, force bool) (item, error) {
	return r.update(ref, store.TaskPatch{}, &to, force)
}

func (r *remoteBackend) remove(ref string) (item, error) {
	if err := checkRemoteRef(ref); err != nil {
		return item{}, err
	}
	var deleted wireTask
	if err := r.call(http.MethodDelete, "/api/tasks/"+url.PathEscape(ref), nil, &deleted); err != nil {
		return item{}, err
	}
	out := fromWireTask(deleted)
	return item{ref: out.ID, task: out}, nil
}

func (r *remoteBackend) view(ref string) (item, []store.Comment, store.TaskLinks, error) {
	if err := checkRemoteRef(ref); err != nil {
		return item{}, nil, store.TaskLinks{}, err
	}
	var detail wireTaskDetail
	if err := r.call(http.MethodGet, "/api/tasks/"+url.PathEscape(ref), nil, &detail); err != nil {
		return item{}, nil, store.TaskLinks{}, err
	}
	t := fromWireTask(detail.wireTask)
	comments := make([]store.Comment, 0, len(detail.Comments))
	for _, wc := range detail.Comments {
		comments = append(comments, fromWireComment(wc))
	}
	links := store.TaskLinks{}
	for _, seq := range detail.Blocks {
		links.Blocks = append(links.Blocks, board.Task{Seq: seq})
	}
	for _, seq := range detail.BlockedBy {
		links.BlockedBy = append(links.BlockedBy, board.Task{Seq: seq})
	}
	return item{ref: t.ID, task: t}, comments, links, nil
}

func (r *remoteBackend) commentAdd(ref, body string) (store.Comment, error) {
	if err := checkRemoteRef(ref); err != nil {
		return store.Comment{}, err
	}
	var created wireComment
	err := r.call(http.MethodPost, "/api/tasks/"+url.PathEscape(ref)+"/comments",
		struct {
			Body string `json:"body"`
		}{body}, &created)
	if err != nil {
		return store.Comment{}, err
	}
	return fromWireComment(created), nil
}

func (r *remoteBackend) comments(ref string) ([]store.Comment, error) {
	if err := checkRemoteRef(ref); err != nil {
		return nil, err
	}
	var listed []wireComment
	if err := r.call(http.MethodGet, "/api/tasks/"+url.PathEscape(ref)+"/comments", nil, &listed); err != nil {
		return nil, err
	}
	out := make([]store.Comment, 0, len(listed))
	for _, wc := range listed {
		out = append(out, fromWireComment(wc))
	}
	return out, nil
}

func (r *remoteBackend) commentRm(id int) (store.Comment, error) {
	var deleted wireComment
	if err := r.call(http.MethodDelete, "/api/comments/"+strconv.Itoa(id), nil, &deleted); err != nil {
		return store.Comment{}, err
	}
	return fromWireComment(deleted), nil
}

func (r *remoteBackend) link(blockerRef, blockedRef string) (board.Task, board.Task, error) {
	if err := checkRemoteRef(blockerRef); err != nil {
		return board.Task{}, board.Task{}, err
	}
	if err := checkRemoteRef(blockedRef); err != nil {
		return board.Task{}, board.Task{}, err
	}
	var out struct {
		Blocker wireTask `json:"blocker"`
		Blocked wireTask `json:"blocked"`
	}
	err := r.call(http.MethodPost, "/api/links",
		struct {
			Blocker string `json:"blocker"`
			Blocked string `json:"blocked"`
		}{blockerRef, blockedRef}, &out)
	if err != nil {
		return board.Task{}, board.Task{}, err
	}
	return fromWireTask(out.Blocker), fromWireTask(out.Blocked), nil
}

func (r *remoteBackend) unlink(aRef, bRef string) error {
	if err := checkRemoteRef(aRef); err != nil {
		return err
	}
	if err := checkRemoteRef(bRef); err != nil {
		return err
	}
	return r.call(http.MethodDelete,
		"/api/links?a="+url.QueryEscape(aRef)+"&b="+url.QueryEscape(bRef), nil, nil)
}

// --- HTTP plumbing ---

const maxWireBody = 4 << 20

// call performs one JSON round-trip. A nil out discards the response body;
// a nil body sends none.
func (r *remoteBackend) call(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, r.base+path, reader)
	if err != nil {
		return err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	req.Header.Set("X-KB-User", r.user)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s%s: %w", method, r.base, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return httpError(method+" "+path, resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWireBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
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
