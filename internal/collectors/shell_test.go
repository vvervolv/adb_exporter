package collectors

import (
	"os/exec"
	"strings"
	"testing"
)

func allMarkers() []string {
	return []string{markerMem, markerBattery, markerUptime, markerDF, markerPower, markerThermal, markerEnd}
}

// TestMarkersHaveNoHash is a cheap invariant: a marker beginning with '#' is
// treated as a comment by the on-device shell and silently dropped.
func TestMarkersHaveNoHash(t *testing.T) {
	for _, m := range allMarkers() {
		if strings.ContainsRune(m, '#') {
			t.Errorf("marker %q contains '#', which starts a shell comment and breaks section splitting", m)
		}
	}
}

// TestShellCommandMarkersSurviveRealShell runs the actual ShellCommand through a
// POSIX shell and verifies every marker is printed. This reproduces (and guards
// against) the class of bug where markers like "###MEM###" are swallowed as
// shell comments, collapsing every section into one and dropping most metrics.
//
// The data commands (cat /proc/*, dumpsys, df) may fail on the test host; only
// the echo markers must reach stdout.
func TestShellCommandMarkersSurviveRealShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available on this host")
	}
	out, _ := exec.Command(sh, "-c", ShellCommand).Output()
	got := string(out)

	for _, m := range allMarkers() {
		if !strings.Contains(got, m) {
			t.Errorf("marker %q missing from real shell output — the shell swallowed it\n--- output ---\n%s", m, got)
		}
	}
}
