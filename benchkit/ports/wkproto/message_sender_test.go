package wkproto_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
)

func TestMessageSenderPortContract(t *testing.T) {
	if wkprotoport.MessageSenderV1 != contract.PortType("port.wkproto.message_sender/v1") {
		t.Fatalf("unexpected port type %q", wkprotoport.MessageSenderV1)
	}
	req := wkprotoport.SendRequest{
		ChannelID:   "u2",
		ChannelType: 1,
		SenderUID:   "u1",
		ClientMsgNo: "msg-1",
		Payload:     []byte("hello"),
		Timeout:     time.Second,
	}
	ack, err := fakeMessageClient{}.SendAndWaitAck(context.Background(), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if ack.MessageID != 7 || ack.MessageSeq != 9 {
		t.Fatalf("unexpected ack: %#v", ack)
	}
}

type fakeMessageClient struct{}

func (fakeMessageClient) SendAndWaitAck(context.Context, wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	return wkprotoport.SendAck{MessageID: 7, MessageSeq: 9}, nil
}
