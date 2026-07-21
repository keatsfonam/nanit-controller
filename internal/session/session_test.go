package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	lastSeen := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	st := NewStore(path)
	if err := st.Update(func(s *Session) {
		s.RefreshToken = "refresh"
		s.AuthToken = "auth"
		s.Babies = []Baby{{UID: "0a1b2c3d", CameraUID: "camera"}}
		s.LastSeenMessageTime = lastSeen
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session mode = %o, want 600", got)
	}

	loaded := NewStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	s := loaded.Snapshot()
	if s.Revision != Revision || s.RefreshToken != "refresh" || s.AuthToken != "auth" || len(s.Babies) != 1 || !s.LastSeenMessageTime.Equal(lastSeen) {
		t.Fatalf("unexpected session: %#v", s)
	}
}

func TestUpdateFailureDoesNotPublishCandidateAndLeavesRecoveryCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	st := NewStore(path)
	if err := st.Update(func(s *Session) { s.RefreshToken = "old" }); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	err := st.Update(func(s *Session) { s.RefreshToken = "rotated" })
	if err == nil || !strings.Contains(err.Error(), "recoverable copy") {
		t.Fatalf("expected recovery-path error, got %v", err)
	}
	if got := st.Snapshot().RefreshToken; got != "old" {
		t.Fatalf("in-memory token = %q after failed commit, want old", got)
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".nanit-session-recovery-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("recovery copies = %v, want one", matches)
	}
	info, err := os.Stat(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("recovery mode = %o, want 600", got)
	}
	var recovery Session
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &recovery); err != nil {
		t.Fatal(err)
	}
	if recovery.RefreshToken != "rotated" {
		t.Fatalf("recovery token = %q, want rotated", recovery.RefreshToken)
	}
}

func TestLoadMigratesLegacyRevisionAndSecuresPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	data := []byte(`{"revision":1,"authToken":"auth","refreshToken":"refresh"}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	st := NewStore(path)
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	if got := st.Snapshot().Revision; got != Revision {
		t.Fatalf("revision = %d, want %d", got, Revision)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session mode = %o, want 600", got)
	}
}

func TestLoadRejectsFutureRevisionAndTrailingJSON(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "future revision", data: `{"revision":99}`},
		{name: "trailing value", data: `{"revision":3} {"revision":3}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.json")
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := NewStore(path).Load(); err == nil {
				t.Fatal("expected load error")
			}
		})
	}
}

func TestSnapshotReturnsIndependentBabies(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "session.json"))
	if err := st.Update(func(s *Session) { s.Babies = []Baby{{UID: "baby"}} }); err != nil {
		t.Fatal(err)
	}
	first := st.Snapshot()
	first.Babies[0].UID = "changed"
	if got := st.Snapshot().Babies[0].UID; got != "baby" {
		t.Fatalf("stored UID changed through snapshot: %q", got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "missing", "session.json"))
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
}
