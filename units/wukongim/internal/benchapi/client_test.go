package benchapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/wkbench/units/wukongim/internal/benchapi"
)

func TestClientPostsTokensWithBearerToken(t *testing.T) {
	var auth string
	var req benchapi.BatchTokensRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/bench/v1/users/tokens" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := benchapi.NewClient(benchapi.Config{APIAddrs: []string{server.URL}, Token: "secret"})
	err := client.UpsertTokens(context.Background(), benchapi.BatchTokensRequest{
		RunID:   "run-1",
		BatchID: "batch-1",
		Upsert:  true,
		Users:   []benchapi.UserTokenItem{{UID: "u1", Token: "t1"}},
	})
	if err != nil {
		t.Fatalf("upsert tokens: %v", err)
	}
	if auth != "Bearer secret" {
		t.Fatalf("unexpected auth %q", auth)
	}
	if req.Users[0].UID != "u1" || req.Users[0].Token != "t1" {
		t.Fatalf("unexpected request: %#v", req)
	}
}
