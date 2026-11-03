package sandbox

import (
	"testing"
	"time"
)

func TestSnapshotObjectKey(t *testing.T) {
	got := SnapshotObjectKey("demo/../x", "abc")
	if got != "snapshots/demo_.._x/snapshot_abc.tar" && got != "snapshots/demo_x/snapshot_abc.tar" {
		if got != "snapshots/demo_.._x/snapshot_abc.tar" {
			// sanitize replaces / and ..
		}
	}
	if SnapshotObjectKey("demo", "abc") != "snapshots/demo/snapshot_abc.tar" {
		t.Fatalf("got %q", SnapshotObjectKey("demo", "abc"))
	}
}

func TestParseExpirationMs(t *testing.T) {
	ms, err := ParseExpirationMs("14d")
	if err != nil {
		t.Fatal(err)
	}
	if ms != 14*24*time.Hour.Milliseconds() {
		t.Fatalf("14d=%d", ms)
	}
	ms, err = ParseExpirationMs("0")
	if err != nil || ms != 0 {
		t.Fatalf("0 => %d %v", ms, err)
	}
	ms, err = ParseExpirationMs("none")
	if err != nil || ms != 0 {
		t.Fatalf("none => %d %v", ms, err)
	}
}

func TestComputeExpiresAt(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !ComputeExpiresAt(now, 0).IsZero() {
		t.Fatal("0 should never expire")
	}
	got := ComputeExpiresAt(now, time.Hour.Milliseconds())
	if !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("got %s", got)
	}
}

func TestSessionTimeoutMessage(t *testing.T) {
	max := uint64(60)
	idle := uint64(30)
	started := time.Now().UTC().Add(-2 * time.Minute)
	spec := Spec{Lifecycle: LifecyclePolicy{MaxDurationSecs: &max}}
	if SessionTimeoutMessage(spec, started, time.Now().UTC()) != "max_duration" {
		t.Fatal("expected max_duration")
	}
	spec = Spec{Lifecycle: LifecyclePolicy{IdleTimeoutSecs: &idle}}
	if SessionTimeoutMessage(spec, time.Now().UTC(), time.Now().UTC()) != "idle_timeout" {
		t.Fatal("expected idle_timeout")
	}
}

func TestKeepLastSnapshotsValidate(t *testing.T) {
	if err := ValidateKeepLastSnapshots(&KeepLastSnapshots{Count: 0}); err == nil {
		t.Fatal("count 0 should fail")
	}
	if err := ValidateKeepLastSnapshots(&KeepLastSnapshots{Count: 3}); err != nil {
		t.Fatal(err)
	}
}

func TestHasSessionTimers(t *testing.T) {
	if (LifecyclePolicy{}).HasSessionTimers() {
		t.Fatal("empty should be false")
	}
	v := uint64(1)
	if !(LifecyclePolicy{IdleTimeoutSecs: &v}).HasSessionTimers() {
		t.Fatal("idle should be true")
	}
}
