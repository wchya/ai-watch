package domain

import "testing"

func TestJobOptionsDefaultsFailureThreshold(t *testing.T) {
	var options JobOptions
	options.Defaults()
	if options.FailureThreshold != 3 {
		t.Fatalf("got failure threshold %d, want 3", options.FailureThreshold)
	}
}
