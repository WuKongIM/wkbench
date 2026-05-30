package fakegroupsender_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
	fakegroupsender "github.com/WuKongIM/wkbench/units/core/fake_group_sender"
)

func TestFakeGroupSenderProducesSenderPort(t *testing.T) {
	env := contract.NewTestRunEnv("run-1", "sender", nil, nil)

	unit := fakegroupsender.Unit{}
	if err := unit.Run(context.Background(), env); err != nil {
		t.Fatalf("run: %v", err)
	}
	sender, err := contract.Output[wkprotoport.GroupSender](env, "sender")
	if err != nil {
		t.Fatalf("sender output: %v", err)
	}
	client, ok := sender.Client("u-1")
	if !ok {
		t.Fatal("expected fake client")
	}
	ack, err := client.SendGroupAndWaitAck(context.Background(), wkprotoport.GroupSendRequest{
		ChannelID:   "g-1",
		SenderUID:   "u-1",
		ClientMsgNo: "m-1",
		Payload:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if ack.MessageID == 0 {
		t.Fatalf("expected fake message id: %#v", ack)
	}
}
