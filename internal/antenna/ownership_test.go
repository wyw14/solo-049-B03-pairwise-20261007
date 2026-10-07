package antenna_test

import (
	"testing"
	"time"

	"orbitlink/internal/antenna"
	"orbitlink/internal/audit"
	"orbitlink/internal/scheduler"
	"orbitlink/internal/store"
)

func newTestFleet(t *testing.T) (*antenna.Fleet, *scheduler.Timeline) {
	t.Helper()
	paths := store.NewPaths(t.TempDir())
	if err := paths.Validate(); err != nil {
		t.Fatal(err)
	}
	trail := audit.NewTrail(store.NewAppendLog(paths))
	timeline, err := scheduler.NewTimeline(paths, trail)
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := antenna.NewFleet(paths, timeline, trail, []string{"ANT-01"})
	if err != nil {
		t.Fatal(err)
	}
	return fleet, timeline
}

func TestClaimRejectsStaleReservationAfterRefresh(t *testing.T) {
	fleet, timeline := newTestFleet(t)
	start := time.Now().UTC()
	stale, err := timeline.Reserve("pass-a", "ANT-01", start, start.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Claim(stale); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Release("ANT-01", "pass-a", stale.Generation); err != nil {
		t.Fatal(err)
	}
	// Replan supersedes the first reservation with a newer generation.
	if _, err := timeline.Release(stale.ReservationID); err != nil {
		t.Fatal(err)
	}
	current, err := timeline.Reserve("pass-a", "ANT-01", start, start.Add(12*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Claim(current); err != nil {
		t.Fatal(err)
	}
	// A delayed caller still holding the old reservation must not take over.
	if _, err := fleet.Claim(stale); err == nil {
		t.Fatal("stale reservation unexpectedly reclaimed the antenna")
	}
	state, found := fleet.State("ANT-01")
	if !found {
		t.Fatal("antenna state missing")
	}
	if state.OwnerPassID != "pass-a" || state.OwnerGeneration != current.Generation {
		t.Fatalf("stale claim rolled ownership back to generation %d", state.OwnerGeneration)
	}
}

func TestClaimRejectsStaleGenerationWhileOwned(t *testing.T) {
	fleet, timeline := newTestFleet(t)
	start := time.Now().UTC()
	stale, err := timeline.Reserve("pass-a", "ANT-01", start, start.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timeline.Release(stale.ReservationID); err != nil {
		t.Fatal(err)
	}
	current, err := timeline.Reserve("pass-a", "ANT-01", start, start.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Claim(current); err != nil {
		t.Fatal(err)
	}
	// The released older generation must not overwrite the live owner,
	// even though it names the same pass.
	if _, err := fleet.Claim(stale); err == nil {
		t.Fatal("older generation unexpectedly overwrote the owner generation")
	}
	state, _ := fleet.State("ANT-01")
	if state.OwnerGeneration != current.Generation {
		t.Fatalf("owner generation regressed to %d", state.OwnerGeneration)
	}
}

func TestClaimRejectsUnknownOrTamperedReservation(t *testing.T) {
	fleet, timeline := newTestFleet(t)
	start := time.Now().UTC()
	reservation, err := timeline.Reserve("pass-a", "ANT-01", start, start.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	unknown := reservation
	unknown.ReservationID = "reservation-never-published"
	if _, err := fleet.Claim(unknown); err == nil {
		t.Fatal("reservation unknown to the timeline unexpectedly claimed the antenna")
	}
	tampered := reservation
	tampered.Generation = reservation.Generation + 1
	if _, err := fleet.Claim(tampered); err == nil {
		t.Fatal("reservation with a forged generation unexpectedly claimed the antenna")
	}
}

func TestClaimLiveReservationIsIdempotent(t *testing.T) {
	fleet, timeline := newTestFleet(t)
	start := time.Now().UTC()
	reservation, err := timeline.Reserve("pass-a", "ANT-01", start, start.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Claim(reservation); err != nil {
		t.Fatal(err)
	}
	state, err := fleet.Claim(reservation)
	if err != nil {
		t.Fatalf("re-claiming the live reservation failed: %v", err)
	}
	if state.OwnerPassID != "pass-a" || state.OwnerGeneration != reservation.Generation {
		t.Fatalf("unexpected owner %s/%d", state.OwnerPassID, state.OwnerGeneration)
	}
}
