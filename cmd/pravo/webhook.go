package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"
)

func postJSON(ctx context.Context, url string, v any) error {
	body, err := jsonText(v, false)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook %s: %s", url, resp.Status)
	}
	return nil
}
