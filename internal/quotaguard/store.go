package quotaguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/filelock"
)

type document struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// Store is shared by the GUI, gateway and terminal processes. No cached
// read-modify-write is allowed. filelock.Mutex intentionally falls open on a
// lock failure; this safety policy uses Lock's held result and refuses instead.
// Network reads MUST happen outside update; Revision rejects late observations.
type Store struct{ Path string }

func (s Store) read() (document, error) {
	d := document{Version: Version, Entries: map[string]Entry{}}
	f, err := os.Open(s.Path)
	var b []byte
	if err == nil {
		b, err = io.ReadAll(io.LimitReader(f, (4<<20)+1))
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		if _, markErr := os.Stat(s.Path + ".enabled"); errors.Is(markErr, os.ErrNotExist) {
			return d, nil
		}
		return d, errors.New("quota guard state is missing after protection was configured; restore the guard file")
	}
	if err != nil {
		return d, fmt.Errorf("read quota guard state: %w", err)
	}
	if len(b) > 4<<20 {
		return d, errors.New("quota guard state exceeds 4 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&d); err != nil {
		return d, fmt.Errorf("quota guard state is unreadable: %w", err)
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return d, errors.New("quota guard state contains trailing data")
	}
	if d.Version != Version || d.Entries == nil {
		return d, errors.New("unsupported quota guard state; do not overwrite it")
	}
	for _, e := range d.Entries {
		if err = e.Policy.Validate(); err != nil {
			return d, fmt.Errorf("invalid saved quota policy: %w", err)
		}
	}
	return d, nil
}

func (s Store) update(change func(*document) (bool, error)) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	unlock, held := filelock.Lock(s.Path+".lock", 250*time.Millisecond)
	if !held {
		return errors.New("quota guard state lock unavailable; protected access was not granted")
	}
	defer unlock()
	d, err := s.read()
	if err != nil {
		return err
	}
	changed, err := change(&d)
	if err != nil || !changed {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > 4<<20 {
		return errors.New("quota guard state exceeds 4 MiB; no write performed")
	}
	if err = os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	// A missing state file is normal before first use, never after enablement.
	// Write the marker first: a crash between writes fails closed on restart.
	if err = edit.WriteAtomic(s.Path+".enabled", []byte("quota-guard-v1\n")); err != nil {
		return err
	}
	return edit.WriteAtomic(s.Path, append(b, '\n'))
}

func (s Store) Get(subject string) (Entry, error) {
	// Before first use, the opt-in feature creates no files or directories.
	// This is only a fast negative read, not authorization to overwrite state.
	if _, err := os.Stat(s.Path); errors.Is(err, os.ErrNotExist) {
		if _, err = os.Stat(s.Path + ".enabled"); errors.Is(err, os.ErrNotExist) {
			return Entry{}, nil
		}
	}
	var e Entry
	err := s.update(func(d *document) (bool, error) { e = d.Entries[subject]; return false, nil })
	return e, err
}

// Configure is explicit user intent. Changing a rule re-evaluates the current
// observation, but an ordinary background refresh never clears an earlier trip.
func (s Store) Configure(subject string, p Policy, expected uint64, now time.Time) (Entry, error) {
	var out Entry
	if subject == "" || len(subject) > 512 {
		return out, errors.New("invalid credential identity")
	}
	if err := p.Validate(); err != nil {
		return out, err
	}
	err := s.update(func(d *document) (bool, error) {
		e := d.Entries[subject]
		if e.Revision != expected {
			return false, ErrConflict
		}
		// A different source mapping cannot inherit measurements from the old
		// JSON pointer/unit, even when the user's metric id is unchanged.
		if e.Policy.BalancePointer != p.BalancePointer || e.Policy.BalanceUnit != p.BalanceUnit {
			e.Snapshots = nil
		}
		e.Policy = p
		e.Revision++
		e.Protection = Protection{LastAction: "configure", ActionAt: &now}
		if p.Enabled {
			Latch(&e, now)
		}
		d.Entries[subject] = e
		out = e
		return true, nil
	})
	return out, err
}

func (s Store) Observe(subject string, revision uint64, snap Snapshot, now time.Time) error {
	if err := ValidateSnapshot(snap, now); err != nil {
		return err
	}
	return s.update(func(d *document) (bool, error) {
		e, ok := d.Entries[subject]
		if !ok || !e.Policy.Enabled {
			return false, nil
		}
		if e.Revision != revision {
			return false, ErrConflict
		}
		// Start order matters, not finish order: a slow old read cannot undo
		// a newer low reading or restore a stale view of the vendor's window.
		if old, ok := e.Snapshots[snap.Source]; ok && !snap.StartedAt.After(old.StartedAt) {
			return false, nil
		}
		if e.Snapshots == nil {
			e.Snapshots = map[string]Snapshot{}
		}
		e.Snapshots[snap.Source] = snap
		Latch(&e, now)
		d.Entries[subject] = e
		return true, nil
	})
}

// HumanAction never touches the user's switch or upstream health. Overrides
// bypass only this opt-in quota policy, not missing/stale data or legacy caps.
func (s Store) HumanAction(subject, action string, until *time.Time, expected uint64, now time.Time) (Entry, error) {
	var out Entry
	err := s.update(func(d *document) (bool, error) {
		e, ok := d.Entries[subject]
		if !ok || !e.Policy.Enabled {
			return false, errors.New("no enabled quota guard on this credential")
		}
		if e.Revision != expected {
			return false, ErrConflict
		}
		switch action {
		case "override":
			if until == nil || !until.After(now) || until.Sub(now) > MaxOverride {
				return false, errors.New("an override must expire within 24 hours")
			}
			if Assess(e, true, now).Unknown {
				return false, errors.New("refresh valid quota data before granting an override")
			}
			e.Protection.OverrideUntil = until
		case "revoke":
			e.Protection.OverrideUntil = nil
		case "rearm":
			probe := e
			probe.Protection = Protection{}
			if !Assess(probe, true, now).Allowed {
				return false, errors.New("quota has not recovered; change the reserve or grant an explicit override")
			}
			e.Protection = Protection{}
		default:
			return false, errors.New("unknown quota guard action")
		}
		e.Revision++
		e.Protection.LastAction = action
		e.Protection.ActionAt = &now
		d.Entries[subject] = e
		out = e
		return true, nil
	})
	return out, err
}
