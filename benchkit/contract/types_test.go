package contract

import (
	"testing"
	"time"
)

func TestTestRunEnvRecordsDurationObservations(t *testing.T) {
	env := NewTestRunEnv("run-1", "traffic", nil, nil)
	env.ObserveDuration("sendack_latency", 10*time.Millisecond, nil)
	env.ObserveDuration("sendack_latency", 20*time.Millisecond, nil)

	values := env.DurationValues("sendack_latency")
	if len(values) != 2 || values[0] != 10*time.Millisecond || values[1] != 20*time.Millisecond {
		t.Fatalf("unexpected duration values: %#v", values)
	}
	values[0] = time.Hour
	if env.DurationValues("sendack_latency")[0] != 10*time.Millisecond {
		t.Fatal("DurationValues must return a copy")
	}
}
