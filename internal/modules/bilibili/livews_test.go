package bilibili

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestEncodePacket(t *testing.T) {
	body := []byte("abc")
	pkt := encodePacket(opEnter, protoInt32, body)

	if len(pkt) != liveWSHeaderSize+len(body) {
		t.Fatalf("packet length = %d, want %d", len(pkt), liveWSHeaderSize+len(body))
	}
	if got := binary.BigEndian.Uint32(pkt[0:4]); got != uint32(len(pkt)) {
		t.Errorf("packLen = %d, want %d", got, len(pkt))
	}
	if got := binary.BigEndian.Uint16(pkt[4:6]); got != liveWSHeaderSize {
		t.Errorf("headerLen = %d, want %d", got, liveWSHeaderSize)
	}
	if got := binary.BigEndian.Uint16(pkt[6:8]); got != protoInt32 {
		t.Errorf("protover = %d, want %d", got, protoInt32)
	}
	if got := binary.BigEndian.Uint32(pkt[8:12]); got != opEnter {
		t.Errorf("operation = %d, want %d", got, opEnter)
	}
	if got := binary.BigEndian.Uint32(pkt[12:16]); got != 1 {
		t.Errorf("sequence = %d, want 1", got)
	}
	if !bytes.Equal(pkt[liveWSHeaderSize:], body) {
		t.Errorf("body = %q, want %q", pkt[liveWSHeaderSize:], body)
	}
}

func collectHandler() (LiveMessageHandler, <-chan []byte) {
	ch := make(chan []byte, 16)
	return func(raw []byte) { ch <- raw }, ch
}

func TestDispatchPlainPackets(t *testing.T) {
	c := NewLiveMessageClient(123, 0, "")
	danmakuFn, danmakuCh := collectHandler()
	giftFn, giftCh := collectHandler()
	c.HandleFunc("DANMU_MSG", danmakuFn)
	c.HandleFunc("SEND_GIFT", giftFn)

	danmakuBody := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1754668800000],"hello",[1,"u"]]}`)
	giftBody := []byte(`{"cmd":"SEND_GIFT","data":{"uname":"g","uid":2,"giftName":"x","num":1}}`)

	payload := append(encodePacket(opMessage, protoPlain, danmakuBody), encodePacket(opMessage, protoPlain, giftBody)...)
	c.dispatch(payload)

	select {
	case raw := <-danmakuCh:
		if !bytes.Equal(raw, danmakuBody) {
			t.Errorf("danmaku body = %q, want %q", raw, danmakuBody)
		}
	case <-time.After(time.Second):
		t.Fatal("DANMU_MSG handler not called")
	}
	select {
	case raw := <-giftCh:
		if !bytes.Equal(raw, giftBody) {
			t.Errorf("gift body = %q, want %q", raw, giftBody)
		}
	case <-time.After(time.Second):
		t.Fatal("SEND_GIFT handler not called")
	}
}

func TestDispatchMalformedPacketLength(t *testing.T) {
	c := NewLiveMessageClient(123, 0, "")
	fn, ch := collectHandler()
	c.HandleFunc("DANMU_MSG", fn)

	good := encodePacket(opMessage, protoPlain, []byte(`{"cmd":"DANMU_MSG","info":[]}`))

	// trailing garbage with impossible packLen must not panic nor dispatch
	bad := append([]byte{}, good...)
	binary.BigEndian.PutUint32(bad[0:4], 1<<20)
	c.dispatch(append(good, bad...))

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("valid leading packet was not dispatched")
	}
	select {
	case raw := <-ch:
		t.Fatalf("malformed packet should not dispatch, got %q", raw)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDispatchCmdColonSuffixNormalization(t *testing.T) {
	c := NewLiveMessageClient(123, 0, "")
	fn, ch := collectHandler()
	c.HandleFunc("DANMU_MSG", fn)

	body := []byte(`{"cmd":"DANMU_MSG:4:0:1:1:1:0","info":[]}`)
	c.dispatch(encodePacket(opMessage, protoPlain, body))

	select {
	case raw := <-ch:
		if !bytes.Equal(raw, body) {
			t.Errorf("body = %q, want %q", raw, body)
		}
	case <-time.After(time.Second):
		t.Fatal("handler not called for colon-suffixed cmd")
	}
}

func TestDispatchBrotliSubPackets(t *testing.T) {
	c := NewLiveMessageClient(123, 0, "")
	danmakuFn, danmakuCh := collectHandler()
	giftFn, giftCh := collectHandler()
	c.HandleFunc("DANMU_MSG", danmakuFn)
	c.HandleFunc("SEND_GIFT", giftFn)

	danmakuBody := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1754668800000],"hello",[1,"u"]]}`)
	giftBody := []byte(`{"cmd":"SEND_GIFT","data":{"uname":"g","uid":2,"giftName":"x","num":1}}`)
	inner := append(encodePacket(opMessage, protoPlain, danmakuBody), encodePacket(opMessage, protoPlain, giftBody)...)

	var compressed bytes.Buffer
	bw := brotli.NewWriter(&compressed)
	if _, err := bw.Write(inner); err != nil {
		t.Fatalf("brotli write: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("brotli close: %v", err)
	}

	c.dispatch(encodePacket(opMessage, protoBrotli, compressed.Bytes()))

	for name, ch := range map[string]<-chan []byte{"DANMU_MSG": danmakuCh, "SEND_GIFT": giftCh} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("%s handler not called from brotli sub-packet", name)
		}
	}
}

func TestDispatchZlibSubPackets(t *testing.T) {
	c := NewLiveMessageClient(123, 0, "")
	fn, ch := collectHandler()
	c.HandleFunc("DANMU_MSG", fn)

	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1754668800000],"z",[1,"u"]]}`)
	inner := encodePacket(opMessage, protoPlain, body)

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(inner); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}

	c.dispatch(encodePacket(opMessage, protoZlib, compressed.Bytes()))

	select {
	case raw := <-ch:
		if !bytes.Equal(raw, body) {
			t.Errorf("body = %q, want %q", raw, body)
		}
	case <-time.After(time.Second):
		t.Fatal("handler not called from zlib sub-packet")
	}
}

func TestHandlerPanicDoesNotKillDispatch(t *testing.T) {
	c := NewLiveMessageClient(123, 0, "")
	c.HandleFunc("DANMU_MSG", func([]byte) { panic("boom") })
	fn, ch := collectHandler()
	c.HandleFunc("DANMU_MSG", fn)

	body := []byte(`{"cmd":"DANMU_MSG","info":[]}`)
	c.dispatch(encodePacket(opMessage, protoPlain, body))

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("second handler not called after first panicked")
	}
}

// fakeDanmakuServer is a plaintext websocket server speaking the bilibili
// danmaku packet protocol for integration-style tests of LiveMessageClient.
type fakeDanmakuServer struct {
	addr string
	stop func()

	enterBody     chan liveEnterPacket
	heartbeatSeen chan struct{}
}

func startFakeDanmakuServer(t *testing.T, onMessage func(conn net.Conn)) *fakeDanmakuServer {
	t.Helper()

	srv := &fakeDanmakuServer{
		enterBody:     make(chan liveEnterPacket, 1),
		heartbeatSeen: make(chan struct{}, 8),
	}

	upgrader := ws.HTTPUpgrader{}
	httpSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, _, _, err := upgrader.Upgrade(r, w)
			if err != nil {
				t.Logf("upgrade: %v", err)
				return
			}
			go srv.serve(conn, onMessage)
		}),
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.addr = ln.Addr().String()
	srv.stop = func() { httpSrv.Close() }
	go httpSrv.Serve(ln)
	return srv
}

func (s *fakeDanmakuServer) serve(conn net.Conn, onMessage func(conn net.Conn)) {
	defer conn.Close()

	// expect enter packet first, then an immediate heartbeat
	msgs, err := wsutil.ReadClientMessage(conn, nil)
	if err != nil {
		return
	}
	for _, msg := range msgs {
		if msg.OpCode != ws.OpBinary || len(msg.Payload) < liveWSHeaderSize {
			return
		}
		if op := binary.BigEndian.Uint32(msg.Payload[8:12]); op != opEnter {
			return
		}
		var enter liveEnterPacket
		if err := json.Unmarshal(msg.Payload[liveWSHeaderSize:], &enter); err != nil {
			return
		}
		s.enterBody <- enter
	}

	msgs, err = wsutil.ReadClientMessage(conn, nil)
	if err != nil {
		return
	}
	for _, msg := range msgs {
		if len(msg.Payload) >= liveWSHeaderSize && binary.BigEndian.Uint32(msg.Payload[8:12]) == opHeartbeat {
			s.heartbeatSeen <- struct{}{}
		}
	}

	// enter response
	enterResp := encodePacket(opEnterResp, protoPlain, []byte(`{"code":0}`))
	if err := wsutil.WriteServerMessage(conn, ws.OpBinary, enterResp); err != nil {
		return
	}

	if onMessage != nil {
		onMessage(conn)
	}
}

func (s *fakeDanmakuServer) host() DanmuInfoHost {
	_, portStr, _ := net.SplitHostPort(s.addr)
	var port int
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	return DanmuInfoHost{Host: "127.0.0.1", WssPort: port}
}

func TestLiveMessageClientRunAgainstFakeServer(t *testing.T) {
	danmakuBody := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1754668800000],"hi",[7,"u"]]}`)
	giftBody := []byte(`{"cmd":"SEND_GIFT","data":{"uname":"g","uid":2,"giftName":"x","num":1}}`)

	server := startFakeDanmakuServer(t, func(conn net.Conn) {
		// one plain packet + one brotli packet carrying a sub-packet
		if err := wsutil.WriteServerMessage(conn, ws.OpBinary, encodePacket(opMessage, protoPlain, danmakuBody)); err != nil {
			return
		}
		var compressed bytes.Buffer
		bw := brotli.NewWriter(&compressed)
		bw.Write(encodePacket(opMessage, protoPlain, giftBody))
		bw.Close()
		wsutil.WriteServerMessage(conn, ws.OpBinary, encodePacket(opMessage, protoBrotli, compressed.Bytes()))

		// keep the connection open until the client disconnects
		buf := make([]byte, 1)
		conn.Read(buf)
	})
	defer server.stop()

	c := NewLiveMessageClient(777, 999, "buvid-test")
	c.plainWS = true
	danmakuFn, danmakuCh := collectHandler()
	giftFn, giftCh := collectHandler()
	c.HandleFunc("DANMU_MSG", danmakuFn)
	c.HandleFunc("SEND_GIFT", giftFn)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- c.Run(ctx, server.host(), "token123")
	}()

	select {
	case enter := <-server.enterBody:
		if enter.RoomID != 777 {
			t.Errorf("enter roomid = %d, want 777", enter.RoomID)
		}
		if enter.UID != 999 {
			t.Errorf("enter uid = %d, want 999", enter.UID)
		}
		if enter.Protover != protoBrotli {
			t.Errorf("enter protover = %d, want %d", enter.Protover, protoBrotli)
		}
		if enter.Buvid != "buvid-test" {
			t.Errorf("enter buvid = %q, want %q", enter.Buvid, "buvid-test")
		}
		if enter.Key != "token123" {
			t.Errorf("enter key = %q, want %q", enter.Key, "token123")
		}
		if enter.Platform != "web" || enter.Type != 2 {
			t.Errorf("enter platform/type = %q/%d, want web/2", enter.Platform, enter.Type)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not receive enter packet")
	}

	select {
	case <-server.heartbeatSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not receive immediate heartbeat")
	}

	select {
	case raw := <-danmakuCh:
		if !bytes.Equal(raw, danmakuBody) {
			t.Errorf("danmaku body = %q, want %q", raw, danmakuBody)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DANMU_MSG not dispatched")
	}
	select {
	case raw := <-giftCh:
		if !bytes.Equal(raw, giftBody) {
			t.Errorf("gift body = %q, want %q", raw, giftBody)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SEND_GIFT not dispatched from brotli packet")
	}

	cancel()
	select {
	case err := <-runDone:
		if err == nil {
			t.Log("Run returned nil after cancel (acceptable)")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestLiveMessageClientServerClose(t *testing.T) {
	server := startFakeDanmakuServer(t, func(conn net.Conn) {
		// close the websocket immediately after entering
		wsutil.WriteServerMessage(conn, ws.OpClose, ws.NewCloseFrameBody(ws.StatusNormalClosure, ""))
	})
	defer server.stop()

	c := NewLiveMessageClient(777, 0, "")
	c.plainWS = true

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.Run(ctx, server.host(), "")
	if err == nil {
		t.Fatal("Run should return an error when the server closes the connection")
	}
	t.Logf("Run returned after server close: %v", err)
}
