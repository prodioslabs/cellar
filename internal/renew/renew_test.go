package renew_test

import (
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/renew"
)

func TestNeeded(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-80 * 24 * time.Hour)
	notAfter := now.Add(10 * 24 * time.Hour) // ~11% remaining of 90d lifetime

	if !renew.Needed(notBefore, notAfter, now, 0.20) {
		t.Fatal("expected renewal needed when <20% remaining")
	}

	notAfterFresh := now.Add(70 * 24 * time.Hour)
	notBeforeFresh := now.Add(-20 * 24 * time.Hour)
	if renew.Needed(notBeforeFresh, notAfterFresh, now, 0.20) {
		t.Fatal("did not expect renewal for fresh cert")
	}

	if !renew.Needed(notBefore, notAfter, notAfter.Add(time.Second), 0.20) {
		t.Fatal("expired cert must need renewal")
	}
}

func TestNextCheck(t *testing.T) {
	now := time.Now()
	nb := now.Add(-time.Hour)
	na := now.Add(100 * time.Hour)
	d := renew.NextCheck(nb, na, now, 0.20)
	if d != time.Hour {
		t.Fatalf("expected hourly check, got %v", d)
	}

	// Already in window.
	na2 := now.Add(time.Hour)
	nb2 := now.Add(-99 * time.Hour)
	d2 := renew.NextCheck(nb2, na2, now, 0.20)
	if d2 != 5*time.Minute {
		t.Fatalf("expected 5m when in window, got %v", d2)
	}
}
