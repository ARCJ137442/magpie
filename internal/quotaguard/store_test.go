package quotaguard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupStore(t *testing.T) (Store, Entry, time.Time) {
	t.Helper()
	s := Store{Path: filepath.Join(t.TempDir(), "quota-guards.json")}
	now := time.Now()
	want := fixture(now, "10")
	e, err := s.Configure("provider/key/fake", want.Policy, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Observe("provider/key/fake", e.Revision, want.Snapshots["windows"], now); err != nil {
		t.Fatal(err)
	}
	e, err = s.Get("provider/key/fake")
	if err != nil {
		t.Fatal(err)
	}
	return s, e, now
}
func TestTripSurvivesReloadAndHumanOverride(t *testing.T) {
	s, e, now := setupStore(t)
	fresh := Store{Path: s.Path}
	e, err := fresh.Get("provider/key/fake")
	if err != nil || !e.Protection.Tripped {
		t.Fatalf("reload: %+v %v", e, err)
	}
	until := now.Add(time.Minute)
	e, err = s.HumanAction("provider/key/fake", "override", &until, e.Revision, now)
	if err != nil {
		t.Fatal(err)
	}
	if !Assess(e, true, now).Allowed || Assess(e, false, now).Allowed {
		t.Fatal("override changed manual intent")
	}
	e, err = s.HumanAction("provider/key/fake", "revoke", nil, e.Revision, now)
	if err != nil || Assess(e, true, now).Allowed {
		t.Fatalf("revoke: %v", err)
	}
}
func TestLateObservationAndLostUpdateRefused(t *testing.T) {
	s, e, now := setupStore(t)
	old := fixture(now.Add(-time.Second), "90").Snapshots["windows"]
	if err := s.Observe("provider/key/fake", e.Revision, old, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("provider/key/fake")
	if err != nil || got.Snapshots["windows"].Metrics[0].Remaining != "10" {
		t.Fatalf("old read won: %+v %v", got, err)
	}
	changed, err := s.Configure("provider/key/fake", e.Policy, e.Revision, now)
	if err != nil {
		t.Fatal(err)
	}
	newer := fixture(now.Add(time.Second), "90").Snapshots["windows"]
	if err = s.Observe("provider/key/fake", e.Revision, newer, now.Add(time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("late revision %v", err)
	}
	if _, err = s.Configure("provider/key/fake", e.Policy, changed.Revision-1, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("lost update %v", err)
	}
}
func TestMissingOrCorruptStateNeverBecomesUnconfigured(t *testing.T) {
	s, _, _ := setupStore(t)
	if err := os.Remove(s.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("provider/key/fake"); err == nil {
		t.Fatal("lost file failed open")
	}
	if err := os.WriteFile(s.Path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("provider/key/fake"); err == nil {
		t.Fatal("corruption failed open")
	}
}
func TestRearmNeedsFreshRecoveredData(t *testing.T) {
	s, e, now := setupStore(t)
	if _, err := s.HumanAction("provider/key/fake", "rearm", nil, e.Revision, now); err == nil {
		t.Fatal("rearmed while below reserve")
	}
	later := now.Add(time.Second)
	snap := fixture(later, "90").Snapshots["windows"]
	if err := s.Observe("provider/key/fake", e.Revision, snap, later); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get("provider/key/fake")
	if err != nil || !e.Protection.Tripped {
		t.Fatal("refresh unlatched")
	}
	e, err = s.HumanAction("provider/key/fake", "rearm", nil, e.Revision, later)
	if err != nil || !Assess(e, true, later).Allowed {
		t.Fatalf("rearm: %v", err)
	}
}
func TestUnusedStoreReadHasNoSideEffects(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-created")
	s := Store{Path: filepath.Join(parent, "guards.json")}
	e, err := s.Get("none")
	if err != nil || e.Revision != 0 {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err = os.Stat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disabled feature created a directory")
	}
}
