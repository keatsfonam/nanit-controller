package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Baby struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	CameraUID string `json:"camera_uid"`
}

type Session struct {
	Revision     int       `json:"revision"`
	AuthToken    string    `json:"authToken"`
	AuthTime     time.Time `json:"authTime"`
	Babies       []Baby    `json:"babies"`
	RefreshToken string    `json:"refreshToken"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	s    Session
}

const Revision = 1

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
	defer f.Close()

	var s Session
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return err
	}
	// Accept older indiefan session revision values if the required fields decode.
	if s.Revision == 0 {
		s.Revision = Revision
	}
	st.s = s
	return nil
}

func (st *Store) Snapshot() Session {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

func (st *Store) Update(fn func(*Session)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	st.s.Revision = Revision
	return st.saveLocked()
}

func (st *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, st.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename session temp file: %w", err)
	}
	return nil
}
