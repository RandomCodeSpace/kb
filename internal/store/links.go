package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// TaskLinks holds one task's outgoing and incoming blocks edges.
type TaskLinks struct {
	Blocks    []board.Task
	BlockedBy []board.Task
}

// Link records "blocker blocks blocked". Both refs accept sequence numbers,
// UUIDs, or unique prefixes. Self-references, duplicate edges, and edges
// that would close a cycle are rejected.
func (s *Store) Link(user, blockerRef, blockedRef string) (blocker, blocked board.Task, err error) {
	err = s.withTx(func(tx *sql.Tx) error {
		blockerID, err := resolveID(tx, user, blockerRef)
		if err != nil {
			return err
		}
		blockedID, err := resolveID(tx, user, blockedRef)
		if err != nil {
			return err
		}
		if blockerID == blockedID {
			return errors.New("a task cannot block itself")
		}
		if blocker, err = getTask(tx, user, blockerID); err != nil {
			return err
		}
		if blocked, err = getTask(tx, user, blockedID); err != nil {
			return err
		}
		cyclic, err := reaches(tx, user, blockedID, blockerID)
		if err != nil {
			return err
		}
		if cyclic {
			return fmt.Errorf("link would create a cycle: #%d already blocks #%d (transitively)", blocked.Seq, blocker.Seq)
		}
		res, err := tx.Exec(`INSERT INTO task_links (scope, blocker_id, blocked_id) VALUES (?, ?, ?)
			ON CONFLICT(scope, blocker_id, blocked_id) DO NOTHING`, user, blockerID, blockedID)
		if err != nil {
			return fmt.Errorf("store: insert link: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return fmt.Errorf("#%d already blocks #%d", blocker.Seq, blocked.Seq)
		}
		return nil
	})
	if err != nil {
		return board.Task{}, board.Task{}, err
	}
	return blocker, blocked, nil
}

// Unlink removes the blocks edge between two tasks, whichever direction it
// points. ErrNotFound when no edge exists.
func (s *Store) Unlink(user, aRef, bRef string) error {
	return s.withTx(func(tx *sql.Tx) error {
		aID, err := resolveID(tx, user, aRef)
		if err != nil {
			return err
		}
		bID, err := resolveID(tx, user, bRef)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM task_links WHERE scope = ? AND
			((blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?))`,
			user, aID, bID, bID, aID)
		if err != nil {
			return fmt.Errorf("store: delete link: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// TaskLinks returns the tasks id blocks and is blocked by, by exact UUID.
func (s *Store) TaskLinks(user, id string) (TaskLinks, error) {
	var out TaskLinks
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		out.Blocks, err = linkedTasks(tx, user,
			`SELECT blocked_id FROM task_links WHERE scope = ? AND blocker_id = ?`, id)
		if err != nil {
			return err
		}
		out.BlockedBy, err = linkedTasks(tx, user,
			`SELECT blocker_id FROM task_links WHERE scope = ? AND blocked_id = ?`, id)
		return err
	})
	if err != nil {
		return TaskLinks{}, err
	}
	return out, nil
}

// linkedTasks loads the tasks on the far end of one edge direction, ordered
// by sequence number.
func linkedTasks(tx *sql.Tx, user, query, id string) ([]board.Task, error) {
	rows, err := tx.Query(query, user, id)
	if err != nil {
		return nil, fmt.Errorf("store: query links: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var other string
		if err := rows.Scan(&other); err != nil {
			return nil, fmt.Errorf("store: scan link: %w", err)
		}
		ids = append(ids, other)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: query links: %w", err)
	}
	out := make([]board.Task, 0, len(ids))
	for _, other := range ids {
		t, err := getTask(tx, user, other)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	sortTasksBySeq(out)
	return out, nil
}

func sortTasksBySeq(tasks []board.Task) {
	for i := 1; i < len(tasks); i++ {
		for j := i; j > 0 && tasks[j-1].Seq > tasks[j].Seq; j-- {
			tasks[j-1], tasks[j] = tasks[j], tasks[j-1]
		}
	}
}

// reaches reports whether "from" can reach "to" following blocks edges —
// the cycle check for Link. Boards are small; BFS in the transaction.
func reaches(tx *sql.Tx, user, from, to string) (bool, error) {
	if from == to {
		return true, nil
	}
	rows, err := tx.Query(`SELECT blocker_id, blocked_id FROM task_links WHERE scope = ?`, user)
	if err != nil {
		return false, fmt.Errorf("store: load links: %w", err)
	}
	defer rows.Close()
	edges := map[string][]string{}
	for rows.Next() {
		var blocker, blocked string
		if err := rows.Scan(&blocker, &blocked); err != nil {
			return false, fmt.Errorf("store: scan link: %w", err)
		}
		edges[blocker] = append(edges[blocker], blocked)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("store: load links: %w", err)
	}
	queue := []string{from}
	seen := map[string]bool{from: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range edges[cur] {
			if next == to {
				return true, nil
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false, nil
}

// openBlockersTx lists the tasks blocking id that are neither done nor
// cancelled — the set that gates finishing it.
func openBlockersTx(tx *sql.Tx, user, id string) ([]board.Task, error) {
	blockers, err := linkedTasks(tx, user,
		`SELECT blocker_id FROM task_links WHERE scope = ? AND blocked_id = ?`, id)
	if err != nil {
		return nil, err
	}
	open := blockers[:0]
	for _, t := range blockers {
		if t.Status != board.StatusDone && t.Status != board.StatusCancelled {
			open = append(open, t)
		}
	}
	return open, nil
}

// describeBlockers names open blockers for the done-refusal message.
func describeBlockers(open []board.Task) string {
	refs := make([]string, 0, len(open))
	for _, t := range open {
		refs = append(refs, displayTaskRef(t))
	}
	noun := "open blocker"
	if len(open) > 1 {
		noun += "s"
	}
	return fmt.Sprintf("%d %s (%s)", len(open), noun, strings.Join(refs, ", "))
}

// displayTaskRef renders a task's user-facing handle: #n, or the UUID for
// pre-backfill rows that somehow lack one.
func displayTaskRef(t board.Task) string {
	if t.Seq > 0 {
		return fmt.Sprintf("#%d", t.Seq)
	}
	return t.ID
}

// reconcileLinksTx drops edges whose endpoints no longer exist — the
// ReplaceBoard companion to the comment and tombstone sweeps.
func reconcileLinksTx(tx *sql.Tx, user string) error {
	if _, err := tx.Exec(`
		DELETE FROM task_links
		WHERE scope = ? AND (
			blocker_id NOT IN (SELECT id FROM tasks WHERE user = ?)
			OR blocked_id NOT IN (SELECT id FROM tasks WHERE user = ?)
		)`, user, user, user); err != nil {
		return fmt.Errorf("store: reconcile links: %w", err)
	}
	return nil
}
