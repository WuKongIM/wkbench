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

// PoolData is the JSON-friendly data representation of an identity pool.
type PoolData struct {
	Items []Identity `json:"items"`
}

// Count implements Pool.
func (p PoolData) Count() int { return len(p.Items) }

// At implements Pool.
func (p PoolData) At(index int) Identity { return p.Items[index] }

// TokenFor implements TokenSource.
func (p PoolData) TokenFor(uid string) (string, bool) {
	for _, item := range p.Items {
		if item.UID == uid {
			return item.Token, item.Token != ""
		}
	}
	return "", false
}

// TokenSource resolves prepared tokens by uid.
type TokenSource interface {
	// TokenFor returns a token for uid when available.
	TokenFor(uid string) (string, bool)
}
