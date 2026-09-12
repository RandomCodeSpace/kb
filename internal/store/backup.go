package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrBackupBusy means another database connection prevents a quiescent copy.
var ErrBackupBusy = errors.New("close every running kb TUI, web, MCP and CLI process before backing up")

// BackupDirectory copies the entire data directory to a new destination.
// No other SQLite connection may be open. A maintenance connection keeps an
// exclusive lock throughout the copy; no transaction is active while copying.
// A failed destination is retained with BackupIncompleteFile, never overwritten
// or removed. The original KB_SECRET remains external when configured.
func BackupDirectory(source, destination string) (err error) {
	return backupDirectory(source, destination, copyBackupFile)
}

func backupDirectory(source, destination string, copyFile func(io.Reader, string) error) (err error) {
	source, destination, err = backupPaths(source, destination)
	if err != nil {
		return err
	}
	path := filepath.Join(source, "kb.db")
	if err := checkPartialCopy(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("backup: inspect source database: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("backup: source kb.db must be a non-empty regular file")
	}
	if _, err := LoadOrCreateSecret(source); err != nil {
		return err
	}
	if err := walkBackupFiles(source, info, nil); err != nil {
		return err
	}
	lock, err := acquireBackupLock(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.close()) }()
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("backup: destination must not exist: %w", err)
	}
	marker := filepath.Join(destination, BackupIncompleteFile)
	if err := copyBackupFile(strings.NewReader("Incomplete kb backup. Do not open this directory.\n"), marker); err != nil {
		return err
	}
	if err := syncSecretDirectory(destination); err != nil {
		return err
	}
	if err := syncSecretDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	var directories []string
	err = walkBackupFiles(source, info, func(path string, entry fs.DirEntry) error {
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			if rel != "." {
				if err := os.Mkdir(target, 0o700); err != nil {
					return err
				}
			}
			directories = append(directories, target)
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		if rel == "kb.db" {
			// On Unix, closing any raw descriptor for kb.db releases this
			// process's SQLite locks. Keep it until the maintenance DB closes.
			lock.input = input
		} else {
			defer input.Close()
		}
		return copyFile(input, target)
	})
	if err != nil {
		return fmt.Errorf("backup: copy incomplete at %s: %w", destination, err)
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := syncSecretDirectory(directories[i]); err != nil {
			return err
		}
	}
	if err := lock.close(); err != nil {
		return err
	}
	if err := os.Remove(marker); err != nil {
		return err
	}
	if err := syncSecretDirectory(destination); err != nil {
		return err
	}
	return syncSecretDirectory(filepath.Dir(destination))
}

func backupPaths(source, destination string) (string, string, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return "", "", err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return "", "", fmt.Errorf("backup: source directory: %w", err)
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return "", "", fmt.Errorf("backup: destination parent must exist: %w", err)
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	rel, err := filepath.Rel(source, destination)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("backup: destination must be outside the source data directory")
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", "", fmt.Errorf("backup: destination %s already exists: %w", destination, os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	return source, destination, nil
}

func walkBackupFiles(source string, database os.FileInfo, visit func(string, fs.DirEntry) error) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("backup: unsupported file %s; symbolic links and special files cannot be copied", path)
		}
		if path != filepath.Join(source, "kb.db") && os.SameFile(info, database) {
			return fmt.Errorf("backup: %s is another link to kb.db; remove the alias before backing up", path)
		}
		if visit != nil {
			return visit(path, entry)
		}
		return nil
	})
}

func copyBackupFile(input io.Reader, target string) error {
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Sync(), output.Close())
}

type backupLock struct {
	db    *sql.DB
	mode  string
	input *os.File
}

func acquireBackupLock(path string) (_ *backupLock, err error) {
	dsn, err := SQLiteDSN(path, "busy_timeout(0)", "synchronous(FULL)")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("mode", "rw") // Never manufacture a missing source database.
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	lock := &backupLock{db: db}
	defer func() {
		if err != nil {
			err = errors.Join(err, lock.close())
		}
	}()
	if err = db.QueryRow(`PRAGMA journal_mode`).Scan(&lock.mode); err != nil {
		return nil, backupLockError(err)
	}
	if _, err = db.Exec(`PRAGMA locking_mode=EXCLUSIVE`); err != nil {
		return nil, backupLockError(err)
	}
	var mode string
	if err = db.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		return nil, backupLockError(err)
	}
	if mode != "delete" {
		return nil, fmt.Errorf("backup: cannot checkpoint database: journal mode is %q", mode)
	}
	if _, err = db.Exec(`BEGIN EXCLUSIVE; COMMIT`); err != nil {
		return nil, backupLockError(err)
	}
	if err = checkDatabaseIntegrity(db); err != nil {
		return nil, err
	}
	return lock, nil
}

func backupLockError(err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("backup: %w", ErrBackupBusy)
	}
	return fmt.Errorf("backup: reserve source database: %w", err)
}

func (lock *backupLock) close() error {
	if lock.db == nil {
		return nil
	}
	// A failed BEGIN/COMMIT sequence may still own a transaction. Restore
	// the persistent journal mode only after rolling that transaction back.
	_, _ = lock.db.Exec(`ROLLBACK`)
	var restoreErr error
	if lock.mode != "" {
		var mode string
		restoreErr = lock.db.QueryRow(`PRAGMA journal_mode=` + lock.mode).Scan(&mode)
		if restoreErr == nil && mode != lock.mode {
			restoreErr = fmt.Errorf("journal mode is %q, want %q", mode, lock.mode)
		}
	}
	if restoreErr != nil {
		restoreErr = fmt.Errorf("backup: restore source journal mode: %w", restoreErr)
	}
	closeErr := lock.db.Close()
	lock.db = nil
	var inputErr error
	if lock.input != nil {
		inputErr = lock.input.Close()
	}
	return errors.Join(restoreErr, closeErr, inputErr)
}
