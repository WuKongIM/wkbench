package preparetokens_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	preparetokens "github.com/WuKongIM/wkbench/units/wukongim/prepare_tokens"
)

func TestPrepareTokensPostsIdentityTokensAndOutputsTokenSource(t *testing.T) {
	var req struct {
		RunID string `json:"run_id"`
		Users []struct {
			UID   string `json:"uid"`
			Token string `json:"token"`
		} `json:"users"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bench/v1/users/tokens" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	identities := identityPool{items: []identityport.Identity{
		{UID: "u1", DeviceID: "d1", Token: "t1"},
		{UID: "u2", DeviceID: "d2", Token: "t2"},
	}}
	env := contract.NewTestRunEnv("run-1", "tokens", map[string]any{
		"target":     targetport.Target{APIAddrs: []string{server.URL}},
		"identities": identities,
	}, map[string]any{"batch_size": 1000})

	if err := (preparetokens.Unit{}).Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	if req.RunID != "run-1" || len(req.Users) != 2 || req.Users[1].Token != "t2" {
		t.Fatalf("unexpected token request: %#v", req)
	}
	source, err := contract.Output[identityport.TokenSource](env, "tokens")
	if err != nil {
		t.Fatalf("tokens output: %v", err)
	}
	if token, ok := source.TokenFor("u1"); !ok || token != "t1" {
		t.Fatalf("unexpected token source result token=%q ok=%v", token, ok)
	}
}

type identityPool struct {
	items []identityport.Identity
}

func (p identityPool) Count() int { return len(p.items) }

func (p identityPool) At(index int) identityport.Identity { return p.items[index] }
