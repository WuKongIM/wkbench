package sessionpool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	protocolcodec "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	protocolenc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
	identityport "github.com/WuKongIM/wkbench/benchkit/ports/identity"
	targetport "github.com/WuKongIM/wkbench/benchkit/ports/target"
	wkprotoport "github.com/WuKongIM/wkbench/benchkit/ports/wkproto"
)

const defaultOperationTimeout = 5 * time.Second

var errClientNotConnected = errors.New("wkproto client: not connected")

// NewProductionClient creates and connects a production WKProto client.
func NewProductionClient(ctx context.Context, target targetport.Target, identity identityport.Identity, token string, gatewayAddr string) (Client, error) {
	timeout := target.OperationTimeout
	if timeout <= 0 {
		timeout = defaultOperationTimeout
	}
	client, err := newWKClient(wkClientConfig{Addr: gatewayAddr, Token: token, OperationTimeout: timeout})
	if err != nil {
		return nil, err
	}
	if err := client.Connect(ctx, identity.UID, identity.DeviceID); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

type wkClientConfig struct {
	Addr             string
	Token            string
	OperationTimeout time.Duration
	Dialer           interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
}

type wkClient struct {
	addr   string
	dialer interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
	operationTimeout time.Duration
	proto            *protocolcodec.WKProto
	token            string

	mu            sync.Mutex
	opMu          sync.Mutex
	writeMu       sync.Mutex
	conn          net.Conn
	privateKey    [32]byte
	publicKey     [32]byte
	sessionCrypto *protocolenc.SessionCrypto
	clientSeq     uint64
}

func newWKClient(cfg wkClientConfig) (*wkClient, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("wkproto client: addr is required")
	}
	if cfg.Dialer == nil {
		cfg.Dialer = &net.Dialer{}
	}
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = defaultOperationTimeout
	}
	private, public, err := protocolenc.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return &wkClient{
		addr:             cfg.Addr,
		dialer:           cfg.Dialer,
		operationTimeout: cfg.OperationTimeout,
		proto:            protocolcodec.New(),
		token:            cfg.Token,
		privateKey:       private,
		publicKey:        public,
	}, nil
}

func (c *wkClient) Connect(ctx context.Context, uid, deviceID string) error {
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	conn, err := c.dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.sessionCrypto = nil
	c.mu.Unlock()

	connect := &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             uid,
		DeviceID:        deviceID,
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
		ClientKey:       protocolenc.EncodePublicKey(c.publicKey),
		Token:           c.token,
	}
	if err := c.writeFrame(ctx, connect); err != nil {
		return err
	}
	f, err := c.readFrame(ctx)
	if err != nil {
		return err
	}
	connack, ok := f.(*frame.ConnackPacket)
	if !ok {
		return fmt.Errorf("wkproto connect: expected connack, got %T", f)
	}
	if connack.ReasonCode != frame.ReasonSuccess {
		return fmt.Errorf("wkproto connect: unexpected reason code %s", connack.ReasonCode)
	}
	return nil
}

func (c *wkClient) SendGroupAndWaitAck(ctx context.Context, req wkprotoport.GroupSendRequest) (wkprotoport.SendAck, error) {
	return c.SendAndWaitAck(ctx, wkprotoport.SendRequest{
		ChannelID:   req.ChannelID,
		ChannelType: 2,
		SenderUID:   req.SenderUID,
		ClientMsgNo: req.ClientMsgNo,
		Payload:     req.Payload,
		Timeout:     req.Timeout,
	})
}

func (c *wkClient) SendAndWaitAck(ctx context.Context, req wkprotoport.SendRequest) (wkprotoport.SendAck, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	ctx, cancel := c.withRequestTimeout(ctx, req.Timeout)
	defer cancel()
	c.clientSeq++
	send := &frame.SendPacket{
		ClientSeq:   c.clientSeq,
		ClientMsgNo: req.ClientMsgNo,
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
		Payload:     req.Payload,
	}
	wireStart := time.Now()
	if err := c.send(ctx, send); err != nil {
		return wkprotoport.SendAck{}, err
	}
	for {
		f, err := c.readFrame(ctx)
		if err != nil {
			return wkprotoport.SendAck{}, err
		}
		ack, ok := f.(*frame.SendackPacket)
		if !ok {
			continue
		}
		if !sendackMatchesRequest(ack, req.ClientMsgNo, send.ClientSeq) {
			continue
		}
		if ack.ReasonCode != frame.ReasonSuccess {
			return wkprotoport.SendAck{}, fmt.Errorf("sendack reason code %s", ack.ReasonCode)
		}
		return wkprotoport.SendAck{
			MessageID:   ack.MessageID,
			MessageSeq:  ack.MessageSeq,
			WireLatency: time.Since(wireStart),
		}, nil
	}
}

func (c *wkClient) Close() error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.sessionCrypto = nil
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (c *wkClient) send(ctx context.Context, pkt *frame.SendPacket) error {
	cloned := *pkt
	if c.cryptoEnabled() && !cloned.Setting.IsSet(frame.SettingNoEncrypt) {
		crypto := c.currentCrypto()
		encrypted, err := protocolenc.EncryptPayloadWithCrypto(cloned.Payload, crypto)
		if err != nil {
			return err
		}
		cloned.Payload = encrypted
		msgKey, err := protocolenc.SendMsgKeyWithCrypto(&cloned, crypto)
		if err != nil {
			return err
		}
		cloned.MsgKey = msgKey
	}
	return c.writeFrame(ctx, &cloned)
}

func (c *wkClient) writeFrame(ctx context.Context, f frame.Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	conn, err := c.currentConn()
	if err != nil {
		return err
	}
	payload, err := c.proto.EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
	if err := withDeadline(ctx, conn.SetWriteDeadline, func() error {
		for len(payload) > 0 {
			n, err := conn.Write(payload)
			if err != nil {
				return err
			}
			payload = payload[n:]
		}
		return nil
	}); err != nil {
		return err
	}
	return ctx.Err()
}

func (c *wkClient) readFrame(ctx context.Context) (frame.Frame, error) {
	conn, err := c.currentConn()
	if err != nil {
		return nil, err
	}
	var f frame.Frame
	if err := withDeadline(ctx, conn.SetReadDeadline, func() error {
		var decodeErr error
		f, decodeErr = c.proto.DecodePacketWithConn(conn, frame.LatestVersion)
		return decodeErr
	}); err != nil {
		return nil, err
	}
	switch pkt := f.(type) {
	case *frame.ConnackPacket:
		if err := c.applyConnack(pkt); err != nil {
			return nil, err
		}
	case *frame.RecvPacket:
		if c.cryptoEnabled() && !pkt.Setting.IsSet(frame.SettingNoEncrypt) {
			plain, err := protocolenc.DecryptPayloadWithCrypto(pkt.Payload, c.currentCrypto())
			if err != nil {
				return nil, err
			}
			pkt.Payload = plain
		}
	}
	return f, ctx.Err()
}

func (c *wkClient) applyConnack(connack *frame.ConnackPacket) error {
	if connack == nil || connack.ServerKey == "" || connack.Salt == "" {
		return nil
	}
	keys, err := protocolenc.DeriveClientSession(c.privateKey, connack.ServerKey, connack.Salt)
	if err != nil {
		return err
	}
	crypto, err := protocolenc.NewSessionCrypto(keys)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.sessionCrypto = crypto
	c.mu.Unlock()
	return nil
}

func (c *wkClient) currentConn() (net.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil, errClientNotConnected
	}
	return c.conn, nil
}

func (c *wkClient) currentCrypto() *protocolenc.SessionCrypto {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionCrypto
}

func (c *wkClient) cryptoEnabled() bool {
	return c.currentCrypto() != nil
}

func (c *wkClient) withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.operationTimeout)
}

func (c *wkClient) withRequestTimeout(ctx context.Context, requested time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	if requested > 0 {
		return context.WithTimeout(ctx, requested)
	}
	return c.withDefaultTimeout(ctx)
}

func sendackMatchesRequest(ack *frame.SendackPacket, clientMsgNo string, clientSeq uint64) bool {
	if ack == nil {
		return false
	}
	if clientMsgNo != "" {
		return ack.ClientMsgNo == clientMsgNo
	}
	return ack.ClientSeq == clientSeq
}

func withDeadline(ctx context.Context, setDeadline func(time.Time) error, run func() error) error {
	deadline, ok := ctx.Deadline()
	if ok {
		if err := setDeadline(deadline); err != nil {
			return err
		}
		defer setDeadline(time.Time{})
	}
	return run()
}
