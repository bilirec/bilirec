package bilibili

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// Bilibili live danmaku websocket protocol constants.
// Verified against blivedm-go (packet/*.go, client/client.go) and
// BililiveRecorder (DanmakuClient.cs).
const (
	liveWSHeaderSize = 16

	opHeartbeat     = 2 // client -> server, empty body
	opHeartbeatResp = 3 // server -> client, 4-byte popularity, ignored
	opMessage       = 5 // server -> client, JSON or compressed sub-packets
	opEnter         = 7 // client -> server, JSON enter body
	opEnterResp     = 8 // server -> client, JSON {"code":0}

	protoPlain  = 0 // plain JSON body
	protoInt32  = 1 // plain JSON body (used by enter/heartbeat)
	protoZlib   = 2 // zlib-compressed sub-packets
	protoBrotli = 3 // brotli-compressed sub-packets

	liveWSHeartbeatInterval = 30 * time.Second
	liveWSReadTimeout       = 70 * time.Second // > 2x heartbeat interval to detect half-dead connections
	liveWSWriteTimeout      = 10 * time.Second
	liveWSDialTimeout       = 10 * time.Second

	maxDecompressedPacketSize = 16 << 20 // decompression bomb guard
)

var (
	liveWSHeartbeatPacket = encodePacket(opHeartbeat, protoInt32, nil)
	liveWSCmdPattern      = regexp.MustCompile(`"cmd":"([^"]+)"`)
)

// LiveMessageHandler receives the raw JSON body of a danmaku message whose
// normalized cmd matches the registered key. It is called synchronously on
// the connection read loop and must not block.
type LiveMessageHandler func(raw []byte)

// LiveMessageClient is a single-lifecycle bilibili live danmaku websocket
// connection. Reconnection is the caller's responsibility. Register handlers
// with HandleFunc before calling Run; HandleFunc is not safe during Run.
type LiveMessageClient struct {
	roomID int
	uid    int
	buvid  string

	handlers map[string][]LiveMessageHandler

	// plainWS dials ws:// instead of wss://; only used by tests against a
	// local plaintext websocket server.
	plainWS bool

	writeMu sync.Mutex // serializes frame writes (read loop control replies + heartbeat)
	connMu  sync.Mutex
	conn    net.Conn

	log logger.Logger
}

func NewLiveMessageClient(roomID, uid int, buvid string) *LiveMessageClient {
	return &LiveMessageClient{
		roomID:   roomID,
		uid:      uid,
		buvid:    buvid,
		handlers: make(map[string][]LiveMessageHandler),
		log:      log.With("room", roomID),
	}
}

// HandleFunc registers a handler for a normalized cmd (e.g. "DANMU_MSG").
func (c *LiveMessageClient) HandleFunc(cmd string, fn LiveMessageHandler) {
	c.handlers[cmd] = append(c.handlers[cmd], fn)
}

// Close force-closes the underlying connection, causing Run to return.
func (c *LiveMessageClient) Close() {
	c.closeConn()
}

// Run dials the given host, enters the room, starts the heartbeat loop and
// blocks dispatching messages until the connection fails or ctx is canceled.
func (c *LiveMessageClient) Run(ctx context.Context, host DanmuInfoHost, token string) error {
	scheme := "wss"
	if c.plainWS {
		scheme = "ws"
	}
	url := fmt.Sprintf("%s://%s:%d/sub", scheme, host.Host, host.WssPort)
	dialer := ws.Dialer{
		Timeout: liveWSDialTimeout,
		Header: ws.HandshakeHeaderHTTP(http.Header{
			"User-Agent": {liveUserAgent},
			"Origin":     {liveOrigin},
			"Referer":    {liveReferer + fmt.Sprint(c.roomID)},
		}),
	}

	conn, br, _, err := dialer.Dial(ctx, url)
	if err != nil {
		return fmt.Errorf("弹幕 websocket 连接失败：%v", err)
	}
	c.setConn(conn)
	defer func() {
		c.setConn(nil)
		conn.Close()
		if br != nil {
			ws.PutReader(br)
		}
	}()

	enterBody, err := json.Marshal(liveEnterPacket{
		UID:      c.uid,
		RoomID:   c.roomID,
		Protover: protoBrotli,
		Buvid:    c.buvid,
		Platform: "web",
		Type:     2,
		Key:      token,
	})
	if err != nil {
		return fmt.Errorf("构造进房封包失败：%v", err)
	}
	if err := c.send(opEnter, protoInt32, enterBody); err != nil {
		return fmt.Errorf("发送进房封包失败：%v", err)
	}
	// Both reference implementations heartbeat immediately after entering,
	// then every 30s; missing heartbeats gets the connection killed server-side.
	if err := c.sendRaw(liveWSHeartbeatPacket); err != nil {
		return fmt.Errorf("发送首次心跳失败：%v", err)
	}

	hbDone := make(chan struct{})
	defer close(hbDone)
	go c.heartbeatLoop(ctx, hbDone)

	var reader io.Reader = conn
	if br != nil {
		reader = br
	}

	for {
		conn.SetReadDeadline(time.Now().Add(liveWSReadTimeout))
		msgs, err := wsutil.ReadServerMessage(reader, nil)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("弹幕 websocket 读取失败：%v", err)
		}
		for _, msg := range msgs {
			if msg.OpCode.IsControl() {
				if msg.OpCode == ws.OpClose {
					return errors.New("服务器关闭了弹幕 websocket 连接")
				}
				c.writeMu.Lock()
				_ = wsutil.HandleServerControlMessage(conn, msg)
				c.writeMu.Unlock()
				continue
			}
			if !msg.OpCode.IsData() {
				continue
			}
			c.dispatch(msg.Payload)
		}
	}
}

func (c *LiveMessageClient) heartbeatLoop(ctx context.Context, done <-chan struct{}) {
	ticker := time.NewTicker(liveWSHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.sendRaw(liveWSHeartbeatPacket); err != nil {
				c.log.Debugf("弹幕心跳发送失败：%v", err)
				return // the read loop notices the broken connection and triggers reconnect
			}
		case <-ctx.Done():
			c.closeConn()
			return
		case <-done:
			return
		}
	}
}

func (c *LiveMessageClient) setConn(conn net.Conn) {
	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
}

func (c *LiveMessageClient) getConn() net.Conn {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.conn
}

func (c *LiveMessageClient) closeConn() {
	if conn := c.getConn(); conn != nil {
		conn.Close()
	}
}

func (c *LiveMessageClient) send(operation uint32, protover uint16, body []byte) error {
	return c.sendRaw(encodePacket(operation, protover, body))
}

func (c *LiveMessageClient) sendRaw(pkt []byte) error {
	conn := c.getConn()
	if conn == nil {
		return errors.New("弹幕 websocket 尚未连接")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(liveWSWriteTimeout))
	return wsutil.WriteClientMessage(conn, ws.OpBinary, pkt)
}

// dispatch splits a websocket message payload into protocol packets. A
// compressed packet expands into multiple concatenated sub-packets.
func (c *LiveMessageClient) dispatch(payload []byte) {
	for len(payload) >= liveWSHeaderSize {
		packLen := int(binary.BigEndian.Uint32(payload[0:4]))
		if packLen < liveWSHeaderSize || packLen > len(payload) {
			c.log.Warnf("弹幕封包长度异常（%d），丢弃剩余 %d 字节", packLen, len(payload))
			return
		}
		protover := binary.BigEndian.Uint16(payload[6:8])
		operation := binary.BigEndian.Uint32(payload[8:12])
		c.handlePacket(protover, operation, payload[liveWSHeaderSize:packLen])
		payload = payload[packLen:]
	}
}

func (c *LiveMessageClient) handlePacket(protover uint16, operation uint32, body []byte) {
	switch operation {
	case opMessage:
		switch protover {
		case protoBrotli:
			data, err := decompressBrotli(body)
			if err != nil {
				c.log.Warnf("弹幕 brotli 解压失败：%v", err)
				return
			}
			c.dispatch(data)
		case protoZlib:
			data, err := decompressZlib(body)
			if err != nil {
				c.log.Warnf("弹幕 zlib 解压失败：%v", err)
				return
			}
			c.dispatch(data)
		default:
			c.dispatchMessage(body)
		}
	case opEnterResp:
		var resp struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			c.log.Warnf("弹幕进房响应解析失败：%v", err)
		} else if resp.Code != 0 {
			c.log.Warnf("弹幕进房响应异常：%s", string(body))
		}
	case opHeartbeatResp:
		// 4-byte popularity value, nothing to do
	}
}

func (c *LiveMessageClient) dispatchMessage(body []byte) {
	m := liveWSCmdPattern.FindSubmatch(body)
	if m == nil {
		return
	}
	cmd := string(m[1])
	// defensive: cmds may carry a colon suffix like "DANMU_MSG:4:0:1:1:1:0"
	if i := strings.IndexByte(cmd, ':'); i >= 0 {
		cmd = cmd[:i]
	}
	for _, fn := range c.handlers[cmd] {
		c.callHandler(cmd, fn, body)
	}
}

func (c *LiveMessageClient) callHandler(cmd string, fn LiveMessageHandler, body []byte) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Errorf("弹幕消息处理器 panic（cmd=%s）：%v", cmd, r)
		}
	}()
	fn(body)
}

type liveEnterPacket struct {
	UID      int    `json:"uid"`
	RoomID   int    `json:"roomid"`
	Protover int    `json:"protover"`
	Buvid    string `json:"buvid"`
	Platform string `json:"platform"`
	Type     int    `json:"type"`
	Key      string `json:"key"`
}

func encodePacket(operation uint32, protover uint16, body []byte) []byte {
	pkt := make([]byte, liveWSHeaderSize+len(body))
	binary.BigEndian.PutUint32(pkt[0:4], uint32(len(pkt)))
	binary.BigEndian.PutUint16(pkt[4:6], liveWSHeaderSize)
	binary.BigEndian.PutUint16(pkt[6:8], protover)
	binary.BigEndian.PutUint32(pkt[8:12], operation)
	binary.BigEndian.PutUint32(pkt[12:16], 1)
	copy(pkt[liveWSHeaderSize:], body)
	return pkt
}

func decompressBrotli(body []byte) ([]byte, error) {
	r := brotli.NewReader(bytes.NewReader(body))
	return io.ReadAll(io.LimitReader(r, maxDecompressedPacketSize))
}

func decompressZlib(body []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, maxDecompressedPacketSize))
}
