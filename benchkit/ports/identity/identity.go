// Package identity defines public identity-related ports.
package identity

import "github.com/WuKongIM/wkbench/benchkit/contract"

// PoolV1 is the port type for deterministic identity pools.
const PoolV1 contract.PortType = "port.identity.pool/v1"

// TokenSourceV1 is the port type for looking up prepared identity tokens.
const TokenSourceV1 contract.PortType = "port.identity.token_source/v1"

// Identity describes one benchmark user session identity.
type Identity struct {
	// UID is the benchmark user id.
	UID string `json:"uid"`
	// DeviceID is the benchmark device id.
	DeviceID string `json:"device_id"`
	// Token is the optional connection token.
	Token string `json:"token,omitempty"`
}

// Pool exposes generated benchmark identities.
type Pool interface {
	// Count returns the number of identities.
	Count() int
	// At returns the identity at index.
	At(index int) Identity
}

// TokenSource resolves prepared tokens by uid.
type TokenSource interface {
	// TokenFor returns a token for uid when available.
	TokenFor(uid string) (string, bool)
}
