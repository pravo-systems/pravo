package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// echoServer accepts one WebSocket and sends every text message back, in
// frames of a size that the test chooses, preceded by a ping.
func echoServer(t *testing.T, fragment bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", wsAccept(key))
		rw.Flush()
		for {
			msg, ok := readClientFrame(rw.Reader)
			if !ok {
				return
			}
			writeServerFrame(conn, 0x9, true, []byte("p"))
			if fragment && len(msg) > 3 {
				writeServerFrame(conn, 0x1, false, msg[:3])
				writeServerFrame(conn, 0x0, true, msg[3:])
			} else {
				writeServerFrame(conn, 0x1, true, msg)
			}
		}
	}))
}

// readClientFrame reads the next text message of the client and skips its
// pongs.
func readClientFrame(r *bufio.Reader) ([]byte, bool) {
	for {
		payload, op, ok := readOneClientFrame(r)
		if !ok {
			return nil, false
		}
		if op == 0x1 {
			return payload, true
		}
		if op != 0xA {
			return nil, false
		}
	}
}

func readOneClientFrame(r *bufio.Reader) ([]byte, byte, bool) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, 0, false
	}
	if h[0]&0x80 == 0 || h[1]&0x80 == 0 {
		return nil, 0, false
	}
	op := h[0] & 0x0f
	l := uint64(h[1] & 0x7f)
	switch l {
	case 126:
		var b [2]byte
		io.ReadFull(r, b[:])
		l = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		io.ReadFull(r, b[:])
		l = binary.BigEndian.Uint64(b[:])
	}
	var mask [4]byte
	io.ReadFull(r, mask[:])
	payload := make([]byte, l)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, 0, false
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return payload, op, true
}

func writeServerFrame(w io.Writer, op byte, fin bool, payload []byte) {
	var buf bytes.Buffer
	b0 := op
	if fin {
		b0 |= 0x80
	}
	buf.WriteByte(b0)
	switch l := len(payload); {
	case l < 126:
		buf.WriteByte(byte(l))
	case l < 1<<16:
		buf.WriteByte(126)
		binary.Write(&buf, binary.BigEndian, uint16(l))
	default:
		buf.WriteByte(127)
		binary.Write(&buf, binary.BigEndian, uint64(l))
	}
	buf.Write(payload)
	w.Write(buf.Bytes())
}

func TestWebSocketRoundTrip(t *testing.T) {
	for _, fragment := range []bool{false, true} {
		srv := echoServer(t, fragment)
		ws, err := dialWS(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/devtools/page/1")
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		for _, n := range []int{0, 1, 125, 126, 1000, 65535, 65536, 200000} {
			want := strings.Repeat("x", n)
			if n > 2 {
				want = "a" + want[1:n-1] + "z"
			}
			if err := ws.writeText([]byte(want)); err != nil {
				t.Fatal(err)
			}
			ws.c.SetReadDeadline(time.Now().Add(5 * time.Second))
			got, err := ws.readMessage()
			if err != nil {
				t.Fatalf("%d bytes, fragmented %v: %v", n, fragment, err)
			}
			if string(got) != want {
				t.Fatalf("%d bytes, fragmented %v: got %d bytes that differ", n, fragment, len(got))
			}
		}
		ws.Close()
		srv.Close()
	}
}

func TestWebSocketRefusedHandshake(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := dialWS(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/x"); err == nil {
		t.Error("a server that does not upgrade: no error")
	}
	if _, err := dialWS(context.Background(), "http://127.0.0.1:1/"); err == nil {
		t.Error("an address that is not ws: no error")
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if _, err := dialWS(context.Background(), "ws://"+addr+"/x"); err == nil {
		t.Error("nothing listens: no error")
	}
}

func TestWebSocketAccept(t *testing.T) {
	// The example of RFC 6455, section 1.3.
	if got := wsAccept("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Errorf("wsAccept = %q", got)
	}
}
