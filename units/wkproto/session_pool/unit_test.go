package sessionpool_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	sessionpool "github.com/WuKongIM/wkbench/units/wkproto/session_pool"
)

func TestSessionPoolConnectsIdentitiesAndOutputsGroupSender(t *testing.T) {
	var connected []connectCall
	unit := sessionpool.Unit{
		ClientFactory: func(ctx context.Context, target targetport.Target, identity identityport.Identity, token string, gatewayAddr string) (sessionpool.Client, error) {
			connected = append(connected, connectCall{uid: identity.UID, token: token, gateway: gatewayAddr})
			return fakeClient{}, nil
		},
	}
	env := contract.NewTestRunEnv("run-1", "sessions", map[string]any{
		"target": targetport.Target{GatewayTCPAddrs: []string{"gw-a", "gw-b"}},
		"identities": identityPool{items: []identityport.Identity{
			{UID: "u1", DeviceID: "d1"},
			{UID: "u2", DeviceID: "d2"},
		}},
		"tokens": tokenSource{tokens: map[string]string{"u1": "t1", "u2": "t2"}},
	}, map[string]any{"connect_rate": "100/s"})

	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(connected) != 2 || connected[0].token != "t1" || connected[1].gateway != "gw-b" {
		t.Fatalf("unexpected connections: %#v", connected)
	}
	sender, err := contract.Output[wkprotoport.GroupSender](env, "group_sender")
	if err != nil {
		t.Fatalf("group_sender output: %v", err)
	}
	if _, ok := sender.Client("u1"); !ok {
		t.Fatal("expected client for u1")
	}
}

type connectCall struct {
	uid     string
	token   string
	gateway string
}

type identityPool struct {
	items []identityport.Identity
}

func (p identityPool) Count() int { return len(p.items) }

func (p identityPool) At(index int) identityport.Identity { return p.items[index] }

type tokenSource struct {
	tokens map[string]string
}

func (s tokenSource) TokenFor(uid string) (string, bool) {
	token, ok := s.tokens[uid]
	return token, ok
}

type fakeClient struct{}

func (fakeClient) SendGroupAndWaitAck(context.Context, wkprotoport.GroupSendRequest) (wkprotoport.SendAck, error) {
	return wkprotoport.SendAck{MessageID: 1}, nil
}

func (fakeClient) Close() error { return nil }
