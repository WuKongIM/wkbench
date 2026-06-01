package sessionpool

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSendackMatchesClientMsgNoBeforeClientSeq(t *testing.T) {
	ack := &frame.SendackPacket{ClientSeq: 7, ClientMsgNo: "other"}
	if sendackMatchesRequest(ack, "expected", 7) {
		t.Fatal("must not match same sequence when request has a different client message number")
	}
	ack.ClientMsgNo = "expected"
	if !sendackMatchesRequest(ack, "expected", 8) {
		t.Fatal("should match client message number even when sequence differs")
	}
}

func TestWithRequestTimeoutPrefersRequestTimeout(t *testing.T) {
	client := &wkClient{operationTimeout: time.Hour}
	ctx, cancel := client.withRequestTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 500*time.Millisecond {
		t.Fatalf("expected request timeout deadline, remaining=%s", remaining)
	}
}
