package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxSessionFileSize = 1 << 20

// Revision matches the session format written by home_assistant_nanit v1.3.5.
const Revision = 3

type Baby struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	CameraUID string `json:"camera_uid"`
}

type Session struct {
	Revision            int       `json:"revision"`
	AuthToken           string    `json:"authToken"`
	AuthTime            time.Time `json:"authTime"`
	Babies              []Baby    `json:"babies"`
	RefreshToken        string    `json:"refreshToken"`
	LastSeenMessageTime time.Time `json:"lastSeenMessageTime,omitempty"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	s    Session
}

func NewStore(path string) *Store {
	return &Store{path: path, s: Session{Revision: Revision}}
}

func (st *Store) Load() error {
	st.mu.Lock()
	defer st.mu.Unlock()

	f, err := os.Open(st.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxSessionFileSize {
		return fmt.Errorf("session file exceeds %d bytes", maxSessionFileSize)
	}

	decoder := json.NewDecoder(f)
	var s Session
	if err := decoder.Decode(&s); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("session file contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing session data: %w", err)
	}

	// Revisions 0-2 are formats previously accepted or written by this project.
	// Reject unknown future revisions instead of silently discarding new fields.
	if s.Revision < 0 || s.Revision > Revision {
		return fmt.Errorf("unsupported session revision %d", s.Revision)
	}
	s.Revision = Revision
	if err := f.Chmod(0o600); err != nil {
		return fmt.Errorf("secure session permissions: %w", err)
	}
	st.s = cloneSession(s)
	return nil
}

func (st *Store) Snapshot() Session {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return cloneSession(st.s)
}

func (st *Store) Update(fn func(*Session)) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	next := cloneSession(st.s)
	fn(&next)
	next.Revision = Revision
	committed, err := st.saveLocked(next)
	if committed {
		st.s = cloneSession(next)
	}
	return err
}

// saveLocked writes a same-directory recovery file, syncs it, renames it over
// the session, and syncs the directory. The boolean reports whether the rename
// committed, even when the final directory sync reports an error.
func (st *Store) saveLocked(s Session) (bool, error) {
	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')

	f, err := os.CreateTemp(dir, ".nanit-session-recovery-*")
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	removeTemp := true
	defer func() {
		_ = f.Close()
		if removeTemp {
			_ = os.Remove(tmp)
		}
	}()

	if err := f.Chmod(0o600); err != nil {
		return false, err
	}
	if n, err := f.Write(data); err != nil {
		return false, err
	} else if n != len(data) {
		return false, io.ErrShortWrite
	}
	// A complete mode-0600 candidate is worth preserving if sync, close, or
	// rename fails: the refresh response may contain the only usable token.
	removeTemp = false
	if err := f.Sync(); err != nil {
		return false, fmt.Errorf("sync session recovery file %q: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close session recovery file %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, st.path); err != nil {
		return false, fmt.Errorf("rename session recovery file (recoverable copy at %q): %w", tmp, err)
	}
	removeTemp = false

	d, err := os.Open(dir)
	if err != nil {
		return true, fmt.Errorf("session committed at %q but open directory for sync: %w", st.path, err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return true, fmt.Errorf("session committed at %q but sync directory: %w", st.path, err)
	}
	if err := d.Close(); err != nil {
		return true, fmt.Errorf("session committed at %q but close directory: %w", st.path, err)
	}
	return true, nil
}

func cloneSession(s Session) Session {
	s.Babies = append([]Baby(nil), s.Babies...)
	return s
}
