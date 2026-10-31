package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

type recordingReporter struct {
	last sandbox.Status
}

func (r *recordingReporter) UpdateStatus(_ context.Context, _ string, _ int64, st sandbox.Status) error {
	r.last = st
	return nil
}

type stopCounter struct {
	stops   int
	removes int
}

func (s *stopCounter) Stop(context.Context, string, int) error { s.stops++; return nil }
func (s *stopCounter) Remove(context.Context, string) error    { s.removes++; return nil }

func TestSessionTimeoutReportsStoppedWithoutReap(t *testing.T) {
	idle := uint64(10)
	sr := &stopCounter{}
	rep := &recordingReporter{}
	a := NewAgent("n1", nil, nil, rep, "")
	a.stopRemove = sr
	a.local["sb1"] = struct{}{}

	sb := &sandbox.Sandbox{
		ID:           "sb1",
		DesiredState: sandbox.DesiredRunning,
		Spec:         sandbox.Spec{Lifecycle: sandbox.LifecyclePolicy{IdleTimeoutSecs: &idle}},
		Status:       sandbox.Status{Phase: sandbox.PhaseRunning, StartedAt: time.Now().UTC().Add(-time.Minute)},
	}
	now := time.Now().UTC()
	msg := sandbox.SessionTimeoutMessage(sb.Spec, sb.Status.StartedAt, now)
	if msg != "idle_timeout" {
		t.Fatalf("msg=%s", msg)
	}
	if err := a.report(context.Background(), sb.ID, 1, sandbox.Status{
		Phase:     sandbox.PhaseStopped,
		Message:   msg,
		StoppedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if sr.removes != 0 {
		t.Fatalf("unexpected reap removes=%d", sr.removes)
	}
	if rep.last.Phase != sandbox.PhaseStopped {
		t.Fatalf("phase=%s", rep.last.Phase)
	}
}

func TestReapRemoves(t *testing.T) {
	sr := &stopCounter{}
	a := NewAgent("n1", nil, nil, nil, "")
	a.stopRemove = sr
	a.local["sb1"] = struct{}{}
	a.reap(context.Background(), "sb1", errors.New("down"))
	if sr.removes != 1 {
		t.Fatalf("removes=%d", sr.removes)
	}
}
