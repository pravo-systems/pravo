package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config names a provider. The key is passed in by the caller, which reads it from
// its environment and nowhere else.
type Config struct {
	// Provider is "anthropic", "openai-compatible" or "fake".
	Provider string
	Model    string
	// Endpoint is the base URL of an openai-compatible provider.
	Endpoint string
	Key      string
}

// Host is where requests go, for the person to read.
func (c Config) Host() string {
	switch c.Provider {
	case "anthropic":
		return "api.anthropic.com"
	case "openai-compatible":
		if u, err := url.Parse(c.Endpoint); err == nil {
			return u.Host
		}
	}
	return ""
}

// New makes the provider of a Config.
func New(c Config) (Provider, error) {
	if c.Model == "" {
		return nil, errors.New("chat: no model named")
	}
	switch c.Provider {
	case "fake":
		return nil, errors.New("chat: the fake provider is made by the test that uses it")
	case "anthropic":
		if c.Key == "" {
			return nil, errors.New("chat: no key: set ANTHROPIC_API_KEY in the environment of pravo serve")
		}
		return &anthropic{c: c, base: "https://api.anthropic.com", hc: client("api.anthropic.com")}, nil
	case "openai-compatible":
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("chat: %q is not an endpoint", c.Endpoint)
		}
		if u.Scheme != "https" && (u.Scheme != "http" || !loopback(u.Hostname())) {
			return nil, errors.New("chat: the endpoint has to be https, or http on this machine")
		}
		return &openai{c: c, base: strings.TrimRight(c.Endpoint, "/"), hc: client(u.Host)}, nil
	}
	return nil, fmt.Errorf("chat: unknown provider %q (anthropic, openai-compatible)", c.Provider)
}

func loopback(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// client talks to one host: a redirect to another is refused, so that a key is
// never sent anywhere else.
func client(host string) *http.Client {
	return &http.Client{
		Timeout: 120 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != host {
				return errors.New("redirected to another host")
			}
			if len(via) > 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// post sends a JSON body and returns the JSON answer. An error carries the status
// and a clipped message of the provider, never a header.
func post(ctx context.Context, hc *http.Client, url string, hdr map[string]string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("the provider could not be reached: %s", clipErr(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the provider answered %d: %s", resp.StatusCode, clip(string(raw), 300))
	}
	return json.Unmarshal(raw, out)
}

func clipErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return clip(ue.Err.Error(), 200)
	}
	return clip(err.Error(), 200)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
