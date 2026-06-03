package identity_test

import (
	"encoding/json"
	"testing"

	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
)

func TestPoolDataJSONRoundTripImplementsPoolAndTokenSource(t *testing.T) {
	source := identityport.PoolData{Items: []identityport.Identity{{
		UID:      "u1",
		DeviceID: "d1",
		Token:    "t1",
	}}}
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded identityport.PoolData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var pool identityport.Pool = decoded
	if pool.Count() != 1 || pool.At(0).UID != "u1" {
		t.Fatalf("decoded pool = %#v", decoded)
	}
	var sourceTokens identityport.TokenSource = decoded
	token, ok := sourceTokens.TokenFor("u1")
	if !ok || token != "t1" {
		t.Fatalf("token = %q, %v", token, ok)
	}
}
