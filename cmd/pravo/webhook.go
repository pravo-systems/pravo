package main

import (
	"bytes"
	"fmt"
	"net/http"
	"time"
)

func postJSON(url string, v any) error {
	body, err := jsonText(v, false)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook %s: %s", url, resp.Status)
	}
	return nil
}
