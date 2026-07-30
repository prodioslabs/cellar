package version

import "testing"

func TestString(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origV, origC, origD
	})

	Version = "1.2.3"
	Commit = "abc1234"
	Date = "2026-07-30T12:00:00Z"

	got := String()
	want := "1.2.3 (commit=abc1234 date=2026-07-30T12:00:00Z)"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestRequested(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"dash", []string{"-version"}, true},
		{"double-dash", []string{"--version"}, true},
		{"among flags", []string{"-data-dir", "/tmp", "--version"}, true},
		{"after terminator", []string{"--", "--version"}, false},
		{"unrelated", []string{"-listen", ":8080"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Requested(tc.args); got != tc.want {
				t.Fatalf("Requested(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
