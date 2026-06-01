package fakemessagesender_test

import (
	"context"
	"testing"

	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	"github.com/WuKongIM/wkbench/benchkit/unittest"
	fakemessagesender "github.com/WuKongIM/wkbench/units/core/fake_message_sender"
)

func TestUnitContract(t *testing.T) {
	unittest.AssertUnitContract(t, fakemessagesender.Unit{})
}

func TestFakeMessageSenderAcknowledgesSends(t *testing.T) {
	sender := &fakemessagesender.Sender{}
	client, ok := sender.MessageClient("u1")
	if !ok {
		t.Fatal("expected client")
	}
	ack, err := client.SendAndWaitAck(context.Background(), wkprotoport.SendRequest{
		ChannelID:   "u2",
		ChannelType: 1,
		SenderUID:   "u1",
		ClientMsgNo: "msg-1",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if ack.MessageID != 1 || ack.MessageSeq != 1 {
		t.Fatalf("unexpected ack: %#v", ack)
	}
}
