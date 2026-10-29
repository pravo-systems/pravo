package main

import "testing"

func TestChatOptionsFromTheEnvironment(t *testing.T) {
	clear := func() {
		for _, k := range []string{"PRAVO_CHAT", "PRAVO_CHAT_MODEL", "PRAVO_CHAT_ENDPOINT", "PRAVO_CHAT_API_KEY", "ANTHROPIC_API_KEY"} {
			t.Setenv(k, "")
		}
	}
	// a key alone turns nothing on
	clear()
	t.Setenv("ANTHROPIC_API_KEY", "k")
	if o, err := chatOptions("", "", ""); o != nil || err != nil {
		t.Fatalf("a key alone: %v %v", o, err)
	}
	// the environment alone
	t.Setenv("PRAVO_CHAT", "anthropic:claude-x")
	o, err := chatOptions("", "", "")
	if err != nil || o == nil || o.Name != "anthropic" || o.Model != "claude-x" || o.Host != "api.anthropic.com" {
		t.Fatalf("env: %+v %v", o, err)
	}
	// a flag wins
	if o, err = chatOptions("", "other", ""); err != nil || o.Model != "other" {
		t.Fatalf("flag model: %+v %v", o, err)
	}
	if o, err = chatOptions("off", "", ""); o != nil || err != nil {
		t.Fatalf("off: %+v %v", o, err)
	}
	// a model is needed, and a key for anthropic
	clear()
	t.Setenv("PRAVO_CHAT", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "k")
	if _, err = chatOptions("", "", ""); err == nil {
		t.Error("no model was accepted")
	}
	clear()
	if _, err = chatOptions("anthropic", "m", ""); err == nil {
		t.Error("no key was accepted for anthropic")
	}
	// a local model, all from the environment; models may contain colons
	clear()
	t.Setenv("PRAVO_CHAT", "openai-compatible:llama3:8b")
	t.Setenv("PRAVO_CHAT_ENDPOINT", "http://localhost:11434/v1")
	if o, err = chatOptions("", "", ""); err != nil || o.Model != "llama3:8b" || o.Host != "localhost:11434" {
		t.Fatalf("local: %+v %v", o, err)
	}
}
