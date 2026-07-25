package runtime

import (
	"testing"
	"time"
)

func TestNextRestartDelay(t *testing.T) {
	want := []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		60 * time.Second,
		60 * time.Second,
		60 * time.Second,
	}
	for i, d := range want {
		got := nextRestartDelay(i)
		if got != d {
			t.Fatalf("attempts=%d: got %v want %v", i, got, d)
		}
	}
	if got := nextRestartDelay(-1); got != 5*time.Second {
		t.Fatalf("negative attempts: got %v", got)
	}
}
