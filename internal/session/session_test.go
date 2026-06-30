package session

import "testing"

func TestStoreLoadSave(t *testing.T) {
	path := t.TempDir() + "/session.json"
	st := NewStore(path)
	if err := st.Update(func(s *Session) {
		s.RefreshToken = "refresh"
		s.AuthToken = "auth"
		s.Babies = []Baby{{UID: "0a1b2c3d", CameraUID: "camera"}}
	}); err != nil {
		t.Fatal(err)
	}
	st2 := NewStore(path)
	if err := st2.Load(); err != nil {
		t.Fatal(err)
	}
	s := st2.Snapshot()
	if s.RefreshToken != "refresh" || s.AuthToken != "auth" || len(s.Babies) != 1 {
		t.Fatalf("unexpected session: %#v", s)
	}
}

func TestLoadMissingFile(t *testing.T) {
	st := NewStore(t.TempDir() + "/missing/session.json")
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
}
