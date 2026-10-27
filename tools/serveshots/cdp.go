package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The browser is driven over the Chrome DevTools Protocol: a WebSocket to the
// page. Its events give the console messages with their levels and its
// commands set the size of the window and the device scale factor, wait for the
// page to settle and take the picture. The few hundred lines of WebSocket
// client below are all that it needs; the module takes no dependency for it.

// wsConn is the client end of a WebSocket.
type wsConn struct {
	c  net.Conn
	br *bufio.Reader
	mu sync.Mutex // serialises writes
}

// dialWS opens a WebSocket to a ws:// address on the loopback interface.
func dialWS(ctx context.Context, address string) (*wsConn, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "ws" {
		return nil, fmt.Errorf("websocket address %q is not a ws:// address", address)
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		c.Close()
		return nil, err
	}
	k := base64.StdEncoding.EncodeToString(key)
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", u.RequestURI(), u.Host, k)
	if _, err := io.WriteString(c, req); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		c.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(k) {
		c.Close()
		return nil, fmt.Errorf("websocket handshake refused: %s", resp.Status)
	}
	return &wsConn{c: c, br: br}, nil
}

// wsAccept is the Sec-WebSocket-Accept value for a key.
func wsAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (w *wsConn) Close() error { return w.c.Close() }

// writeText sends one text message, masked as a client must.
func (w *wsConn) writeText(data []byte) error {
	var hdr [14]byte
	hdr[0] = 0x80 | 0x1
	n := 2
	switch l := len(data); {
	case l < 126:
		hdr[1] = 0x80 | byte(l)
	case l < 1<<16:
		hdr[1] = 0x80 | 126
		binary.BigEndian.PutUint16(hdr[2:], uint16(l))
		n = 4
	default:
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(l))
		n = 10
	}
	mask := hdr[n : n+4]
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	n += 4
	masked := make([]byte, len(data))
	for i, b := range data {
		masked[i] = b ^ mask[i%4]
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.c.Write(hdr[:n]); err != nil {
		return err
	}
	_, err := w.c.Write(masked)
	return err
}

// readMessage reads one message, joining fragments. It answers a ping and
// returns io.EOF when the peer closes.
func (w *wsConn) readMessage() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		masked := h[1]&0x80 != 0
		l := uint64(h[1] & 0x7f)
		switch l {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, err
			}
			l = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, err
			}
			l = binary.BigEndian.Uint64(b[:])
		}
		if l > 256<<20 {
			return nil, fmt.Errorf("websocket frame of %d bytes", l)
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.br, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, l)
		if _, err := io.ReadFull(w.br, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case 0x0, 0x1, 0x2:
			msg = append(msg, payload...)
			if fin {
				return msg, nil
			}
		case 0x8:
			return nil, io.EOF
		case 0x9:
			w.writeControl(0xA, payload)
		}
	}
}

// writeControl sends a pong, masked.
func (w *wsConn) writeControl(op byte, payload []byte) {
	if len(payload) > 125 {
		return
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	frame := []byte{0x80 | op, 0x80 | byte(len(payload))}
	frame = append(frame, mask...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.Write(frame)
}

// cdpEvent is a message of the protocol that no command asked for.
type cdpEvent struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// cdpClient sends commands and collects events.
type cdpClient struct {
	ws      *wsConn
	mu      sync.Mutex
	nextID  int
	pending map[int]chan cdpReply
	events  chan cdpEvent
	done    chan struct{}
	err     error
}

type cdpReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func newCDP(ws *wsConn) *cdpClient {
	c := &cdpClient{ws: ws, pending: map[int]chan cdpReply{}, events: make(chan cdpEvent, 4096), done: make(chan struct{})}
	go c.read()
	return c
}

func (c *cdpClient) read() {
	defer close(c.done)
	for {
		msg, err := c.ws.readMessage()
		if err != nil {
			c.mu.Lock()
			c.err = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var m struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			cdpReply
		}
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		if m.ID != nil {
			c.mu.Lock()
			ch := c.pending[*m.ID]
			delete(c.pending, *m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m.cdpReply
			}
			continue
		}
		select {
		case c.events <- cdpEvent{Method: m.Method, Params: m.Params}:
		default:
		}
	}
}

// call runs a command and returns its result.
func (c *cdpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan cdpReply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	body, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := c.ws.writeText(body); err != nil {
		return nil, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("%s: the browser closed the connection", method)
		}
		if r.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, r.Error.Message)
		}
		return r.Result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// consoleLog collects what the page logs.
type consoleLog struct {
	Errors []string
	Notes  []string
}

// add reads one event and keeps the messages: a console.error, an exception
// that nothing caught, and an error that the browser logs (a failed request, a
// refused policy) are errors; any other console message is a note.
func (l *consoleLog) add(ev cdpEvent) {
	switch ev.Method {
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Type        string `json:"type"`
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
		}
		if json.Unmarshal(ev.Params, &p) != nil {
			return
		}
		var parts []string
		for _, a := range p.Args {
			switch {
			case a.Value != nil:
				parts = append(parts, fmt.Sprint(a.Value))
			case a.Description != "":
				parts = append(parts, a.Description)
			default:
				parts = append(parts, a.Type)
			}
		}
		text := strings.Join(parts, " ")
		switch p.Type {
		case "error", "assert":
			l.Errors = append(l.Errors, "console."+p.Type+": "+text)
		case "log", "info", "debug", "warning", "warn":
			l.Notes = append(l.Notes, "console."+p.Type+": "+text)
		}
	case "Runtime.exceptionThrown":
		var p struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				URL       string `json:"url"`
				Line      int    `json:"lineNumber"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(ev.Params, &p) != nil {
			return
		}
		d := p.ExceptionDetails
		text := d.Exception.Description
		if text == "" {
			text = d.Text
		}
		l.Errors = append(l.Errors, fmt.Sprintf("exception: %s (%s:%d)", firstLineOf(text), d.URL, d.Line))
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
			} `json:"entry"`
		}
		if json.Unmarshal(ev.Params, &p) != nil {
			return
		}
		e := p.Entry
		if strings.Contains(e.URL, "favicon.ico") || strings.Contains(e.Text, "favicon.ico") {
			return
		}
		text := e.Text
		if e.URL != "" {
			text += " (" + e.URL + ")"
		}
		switch e.Level {
		case "error":
			l.Errors = append(l.Errors, "browser: "+text)
		case "warning":
			l.Notes = append(l.Notes, "browser: "+text)
		}
	}
}

func firstLineOf(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// browser is a headless Chrome that was started for one shot.
type browser struct {
	port   string
	stderr *syncBuffer
	stop   func()
}

// pageSocket returns the WebSocket address of the first page of the browser.
func (b *browser) pageSocket(ctx context.Context) (string, error) {
	var last error
	for range 50 {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+b.port+"/json/list", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			var targets []struct {
				Type string `json:"type"`
				WS   string `json:"webSocketDebuggerUrl"`
			}
			err = json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			if err == nil {
				for _, t := range targets {
					if t.Type == "page" && t.WS != "" {
						return t.WS, nil
					}
				}
				err = errors.New("the browser has no page")
			}
		}
		last = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("the browser does not list a page: %v", last)
}

// settleScript reads what the page shows. The page has settled when it reads
// the same for a while: the interface lays out its graph and draws it after the
// data arrives, and nothing else says when it is done.
const settleScript = `(() => {
  const b = document.body;
  if (!b) return "";
  const t = b.innerText || "";
  return [document.readyState, t.length, b.getElementsByTagName("*").length, document.querySelectorAll("svg path, svg rect, svg text").length, t.slice(0, 400)].join("|");
})()`

// waitSettled polls the page until its reading is the same for stable
// successive polls, and at least min has passed, or max has.
func waitSettled(ctx context.Context, c *cdpClient, interval time.Duration, stable int, min, max time.Duration) error {
	start := time.Now()
	last, same := "", 0
	for {
		raw, err := c.call(ctx, "Runtime.evaluate", map[string]any{"expression": settleScript, "returnByValue": true})
		if err != nil {
			return err
		}
		var r struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if r.Result.Value == last && strings.HasPrefix(r.Result.Value, "complete|") {
			same++
		} else {
			same, last = 0, r.Result.Value
		}
		elapsed := time.Since(start)
		if same >= stable && elapsed >= min {
			return nil
		}
		if elapsed >= max {
			return fmt.Errorf("the page did not settle within %s", max)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// drainEvents moves the events that arrived into the log.
func drainEvents(c *cdpClient, log *consoleLog) {
	for {
		select {
		case ev := <-c.events:
			log.add(ev)
		default:
			return
		}
	}
}

// decodeScreenshot reads the PNG out of the result of Page.captureScreenshot.
func decodeScreenshot(raw json.RawMessage) ([]byte, error) {
	var r struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG")) {
		return nil, errors.New("the browser returned data that is not a PNG")
	}
	return data, nil
}
