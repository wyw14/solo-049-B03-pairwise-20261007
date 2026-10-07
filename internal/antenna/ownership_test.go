package antenna

import (
	"testing"
	"time"

	"orbitlink/internal/audit"
	"orbitlink/internal/scheduler"
	"orbitlink/internal/store"
)

func newTestFleet(t *testing.T) (*Fleet, *scheduler.Timeline) {
	t.Helper()
	paths := store.NewPaths(t.TempDir())
	trail := audit.NewTrail(store.NewAppendLog(paths))
	timeline, err := scheduler.NewTimeline(paths, trail)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	fleet, err := NewFleet(paths, timeline, trail, []string{"ANT-01"})
	if err != nil {
		t.Fatalf("fleet: %v", err)
	}
	return fleet, timeline
}

func reserve(t *testing.T, timeline *scheduler.Timeline, passID string) scheduler.Interval {
	t.Helper()
	start := time.Now().UTC()
	reservation, err := timeline.Reserve(passID, "ANT-01", start, start.Add(8*time.Minute))
	if err != nil {
		t.Fatalf("reserve %s: %v", passID, err)
	}
	return reservation
}

// A stale reservation must not overwrite the owner generation once a newer
// reservation has claimed the antenna.
func TestClaimRejectsStaleGenerationAfterRefresh(t *testing.T) {
	fleet, timeline := newTestFleet(t)

	stale := reserve(t, timeline, "PASS-1")
	if _, err := fleet.Claim(stale); err != nil {
		t.Fatalf("initial claim: %v", err)
	}

	// Refresh: release the old interval and reserve a newer generation.
	if _, err := timeline.Release(stale.ReservationID); err != nil {
		t.Fatalf("release: %v", err)
	}
	current := reserve(t, timeline, "PASS-1")
	if current.Generation <= stale.Generation {
		t.Fatalf("expected newer generation, got %d after %d", current.Generation, stale.Generation)
	}
	if _, err := fleet.Claim(current); err != nil {
		t.Fatalf("claim of current generation: %v", err)
	}

	// A late retry of the stale reservation must not take over.
	if _, err := fleet.Claim(stale); err == nil {
		t.Fatalf("stale reservation claimed the antenna")
	}
	state, _ := fleet.State("ANT-01")
	if state.OwnerPassID != "PASS-1" || state.OwnerGeneration != current.Generation {
		t.Fatalf("stale claim overwrote owner: %+v", state)
	}
	// The current owner must still be able to release cleanly.
	if err := fleet.Release("ANT-01", current.PassID, current.Generation); err != nil {
		t.Fatalf("release after stale claim attempt: %v", err)
	}
}

// A reservation that was released in the timeline must not claim an idle
// antenna, even when no owner currently occupies it.
func TestClaimRejectsReleasedReservation(t *testing.T) {
	fleet, timeline := newTestFleet(t)

	reservation := reserve(t, timeline, "PASS-1")
	if _, err := timeline.Release(reservation.ReservationID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := fleet.Claim(reservation); err == nil {
		t.Fatalf("released reservation claimed the antenna")
	}
	state, _ := fleet.State("ANT-01")
	if state.OwnerPassID != "" {
		t.Fatalf("released claim set owner: %+v", state)
	}
}

// A caller-held copy whose generation does not match the timeline record must
// not claim the antenna.
func TestClaimRejectsForgedGeneration(t *testing.T) {
	fleet, timeline := newTestFleet(t)

	reservation := reserve(t, timeline, "PASS-1")
	forged := reservation
	forged.Generation += 10
	if _, err := fleet.Claim(forged); err == nil {
		t.Fatalf("forged generation claimed the antenna")
	}
	if _, err := fleet.Claim(reservation); err != nil {
		t.Fatalf("honest claim rejected: %v", err)
	}
	state, _ := fleet.State("ANT-01")
	if state.OwnerGeneration != reservation.Generation {
		t.Fatalf("owner generation = %d, want %d", state.OwnerGeneration, reservation.Generation)
	}
}

// An older same-pass reservation must not displace a newer owner even while
// its own interval is still reserved in the timeline (non-overlapping
// windows): the device occupancy check is what rejects it.
func TestClaimRejectsOlderGenerationWhileStillReserved(t *testing.T) {
	fleet, timeline := newTestFleet(t)

	start := time.Now().UTC()
	older, err := timeline.Reserve("PASS-1", "ANT-01", start, start.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("reserve older: %v", err)
	}
	newer, err := timeline.Reserve("PASS-1", "ANT-01", start.Add(10*time.Minute), start.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("reserve newer: %v", err)
	}
	if _, err := fleet.Claim(newer); err != nil {
		t.Fatalf("claim newer: %v", err)
	}
	if _, err := fleet.Claim(older); err == nil {
		t.Fatalf("older generation displaced newer owner")
	}
	state, _ := fleet.State("ANT-01")
	if state.OwnerPassID != "PASS-1" || state.OwnerGeneration != newer.Generation {
		t.Fatalf("older claim overwrote owner: %+v", state)
	}
}
