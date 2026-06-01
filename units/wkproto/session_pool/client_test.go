package sessionpool

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	protocolcodec "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
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

func TestSendAndWaitAckUsesRequestedChannelType(t *testing.T) {
	proto := protocolcodec.New()
	ackBytes, err := proto.EncodeFrame(&frame.SendackPacket{
		ClientSeq:   1,
		ClientMsgNo: "msg-1",
		ReasonCode:  frame.ReasonSuccess,
		MessageID:   42,
		MessageSeq:  3,
	}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("encode ack: %v", err)
	}
	conn := &bufferConn{read: bytes.NewReader(ackBytes)}
	client := &wkClient{conn: conn, proto: proto, operationTimeout: time.Second}

	ack, err := client.SendAndWaitAck(context.Background(), wkprotoport.SendRequest{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		SenderUID:   "u1",
		ClientMsgNo: "msg-1",
		Payload:     []byte("hello"),
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if ack.MessageID != 42 || ack.MessageSeq != 3 {
		t.Fatalf("unexpected ack: %#v", ack)
	}
	written := conn.written.Bytes()
	decoded, _, err := proto.DecodeFrame(written, frame.LatestVersion)
	if err != nil {
		t.Fatalf("decode written frame: %v", err)
	}
	send, ok := decoded.(*frame.SendPacket)
	if !ok {
		t.Fatalf("expected send packet, got %T", decoded)
	}
	if send.ChannelID != "u2" || send.ChannelType != frame.ChannelTypePerson || send.ClientMsgNo != "msg-1" {
		t.Fatalf("unexpected send packet: %#v", send)
	}
}

type bufferConn struct {
	read    *bytes.Reader
	written bytes.Buffer
}

func (c *bufferConn) Read(p []byte) (int, error)       { return c.read.Read(p) }
func (c *bufferConn) Write(p []byte) (int, error)      { return c.written.Write(p) }
func (c *bufferConn) Close() error                     { return nil }
func (c *bufferConn) LocalAddr() net.Addr              { return dummyAddr("local") }
func (c *bufferConn) RemoteAddr() net.Addr             { return dummyAddr("remote") }
func (c *bufferConn) SetDeadline(time.Time) error      { return nil }
func (c *bufferConn) SetReadDeadline(time.Time) error  { return nil }
func (c *bufferConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }
