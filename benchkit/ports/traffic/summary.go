// Package traffic defines traffic result ports.
package traffic

import "github.com/WuKongIM/wkbench/benchkit/contract"

// SummaryV1 is the port type for traffic summaries.
const SummaryV1 contract.PortType = "port.traffic.summary/v1"

// Summary is a compact traffic result consumed by report units.
type Summary struct {
	// SendackOK counts successful sendack waits.
	SendackOK uint64 `json:"sendack_ok"`
	// SendackErrors counts failed sendack waits.
	SendackErrors uint64 `json:"sendack_errors"`
	// ElapsedMS records the measured traffic-unit runtime in milliseconds.
	ElapsedMS int64 `json:"elapsed_ms,omitempty"`
	// LastMessageID records the last acknowledged message id when present.
	LastMessageID int64 `json:"last_message_id,omitempty"`
}

// SendackErrorRate returns failed sendack waits divided by all send attempts.
func (s Summary) SendackErrorRate() float64 {
	total := s.SendackOK + s.SendackErrors
	if total == 0 {
		return 0
	}
	return float64(s.SendackErrors) / float64(total)
}

// ActualQPS returns successful sendacks divided by the measured runtime.
func (s Summary) ActualQPS() float64 {
	if s.ElapsedMS <= 0 {
		return 0
	}
	return float64(s.SendackOK) / (float64(s.ElapsedMS) / 1000)
}

// ReportOutput implements contract.ReportableOutput.
func (s Summary) ReportOutput() any {
	return s
}
