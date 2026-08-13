package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// Comment is one comment on a task. ID is the per-board stable comment
// number (displayed "c<n>"), assigned once and never reused. TaskSeq is the
// owning task's stable number, carried for display.
type Comment struct {
	ID        int
	TaskID    string
	TaskSeq   int
	Author    string
	Body      string
	CreatedAt time.Time
}

// nextCommentID allocates the next per-board comment id inside the caller's
// transaction; like task sequence numbers, the counter only advances.
func nextCommentID(q dbtx, user string) (int, error) {
	if _, err := q.Exec(`INSERT INTO comment_sequences(user, next) VALUES (?, 2)
		ON CONFLICT(user) DO UPDATE SET next = next + 1`, user); err != nil {
		return 0, fmt.Errorf("store: advance comment sequence: %w", err)
	}
	var next int
	if err := q.QueryRow(`SELECT next FROM comment_sequences WHERE user = ?`, user).Scan(&next); err != nil {
		return 0, fmt.Errorf("store: read comment sequence: %w", err)
	}
	return next - 1, nil
}

// Task fetches one task by reference (sequence number, UUID, or unique
// prefix) without mutating anything.
func (s *Store) Task(user, ref string) (board.Task, error) {
	var out board.Task
	err := s.withTx(func(tx *sql.Tx) error {
		id, err := resolveID(tx, user, ref)
		if err != nil {
			return err
		}
		out, err = getTask(tx, user, id)
		return err
	})
	if err != nil {
		return board.Task{}, err
	}
	return out, nil
}

// AddComment appends a comment to the task matching taskRef (sequence
// number, UUID, or unique prefix) and returns it with its assigned id.
func (s *Store) AddComment(user, taskRef, author, body string) (Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Comment{}, errors.New("store: comment body must not be empty")
	}
	var out Comment
	err := s.withTx(func(tx *sql.Tx) error {
		id, err := resolveID(tx, user, taskRef)
		if err != nil {
			return err
		}
		t, err := getTask(tx, user, id)
		if err != nil {
			return err
		}
		cid, err := nextCommentID(tx, user)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.Exec(`INSERT INTO comments (scope, id, task_id, author, body, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			user, cid, id, author, body, now.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("store: insert comment: %w", err)
		}
		out = Comment{ID: cid, TaskID: id, TaskSeq: t.Seq, Author: author, Body: body, CreatedAt: now}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	return out, nil
}

// Comments lists a task's comments oldest-first.
func (s *Store) Comments(user, taskRef string) ([]Comment, error) {
	var out []Comment
	err := s.withTx(func(tx *sql.Tx) error {
		id, err := resolveID(tx, user, taskRef)
		if err != nil {
			return err
		}
		t, err := getTask(tx, user, id)
		if err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT id, author, body, created_at FROM comments
			WHERE scope = ? AND task_id = ? ORDER BY id`, user, id)
		if err != nil {
			return fmt.Errorf("store: list comments: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c := Comment{TaskID: id, TaskSeq: t.Seq}
			var created string
			if err := rows.Scan(&c.ID, &c.Author, &c.Body, &created); err != nil {
				return fmt.Errorf("store: scan comment: %w", err)
			}
			if c.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
				return fmt.Errorf("store: comment c%d created_at: %w", c.ID, err)
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteComment removes comment id ("7" or "c7" accepted by callers, parsed
// to the integer here) and returns it. ErrNotFound when no such comment.
func (s *Store) DeleteComment(user string, id int) (Comment, error) {
	var out Comment
	err := s.withTx(func(tx *sql.Tx) error {
		var created string
		err := tx.QueryRow(`SELECT id, task_id, author, body, created_at FROM comments
			WHERE scope = ? AND id = ?`, user, id).
			Scan(&out.ID, &out.TaskID, &out.Author, &out.Body, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("store: load comment: %w", err)
		}
		if out.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return fmt.Errorf("store: comment c%d created_at: %w", id, err)
		}
		var t board.Task
		if t, err = getTask(tx, user, out.TaskID); err == nil {
			out.TaskSeq = t.Seq
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM comments WHERE scope = ? AND id = ?`, user, id); err != nil {
			return fmt.Errorf("store: delete comment: %w", err)
		}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	return out, nil
}

// reconcileCommentsTx drops comments whose task no longer exists — the
// ReplaceBoard companion to reconcileBoardTombstonesTx.
func reconcileCommentsTx(tx *sql.Tx, user string) error {
	if _, err := tx.Exec(`
		DELETE FROM comments
		WHERE scope = ? AND task_id NOT IN (SELECT id FROM tasks WHERE user = ?)`,
		user, user); err != nil {
		return fmt.Errorf("store: reconcile comments: %w", err)
	}
	return nil
}
