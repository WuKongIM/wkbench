// Package target defines public target endpoint ports.
package target

import (
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

// TargetV1 is the port type for black-box target endpoints.
const TargetV1 contract.PortType = "port.target.endpoint/v1"

// Target describes black-box WuKongIM endpoints needed by units.
type Target struct {
	// APIAddrs are HTTP API base addresses.
	APIAddrs []string `json:"api_addrs"`
	// GatewayTCPAddrs are WKProto TCP gateway addresses.
	GatewayTCPAddrs []string `json:"gateway_tcp_addrs"`
	// BenchAPIToken is the optional bearer token for /bench/v1 routes.
	BenchAPIToken string `json:"bench_api_token,omitempty"`
	// OperationTimeout bounds HTTP and WKProto operations.
	OperationTimeout time.Duration `json:"operation_timeout,omitempty"`
}
