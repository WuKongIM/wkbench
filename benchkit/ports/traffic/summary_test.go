package traffic

import "testing"

func TestSummaryActualQPSUsesElapsedMilliseconds(t *testing.T) {
	summary := Summary{SendackOK: 900, ElapsedMS: 1500}

	if got, want := summary.ActualQPS(), 600.0; got != want {
		t.Fatalf("ActualQPS() = %v, want %v", got, want)
	}
}

func TestSummaryActualQPSHandlesMissingElapsed(t *testing.T) {
	summary := Summary{SendackOK: 900}

	if got := summary.ActualQPS(); got != 0 {
		t.Fatalf("ActualQPS() without elapsed = %v, want 0", got)
	}
}
