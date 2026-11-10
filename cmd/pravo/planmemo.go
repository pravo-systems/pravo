package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/protect"
)

// planMode is PRAVO_PLAN: "" for a plan that uses its memo, "full" for one that
// evaluates every resource, "check" for one that does both and compares.
var planMode string

// applyPlanMode sets how a plan uses the memo of the plans before it, from
// PRAVO_PLAN. Unset, empty or "memo" leaves a resource alone without evaluating
// its configuration when nothing the configuration is made from has changed;
// "full" evaluates every resource, as a plan without a memo does; "check" does
// both and fails when they differ.
func applyPlanMode(e env.Env) error {
	switch v := e.Get("PRAVO_PLAN"); v {
	case "", "memo":
		planMode = ""
	case "full", "check":
		planMode = v
	default:
		return fmt.Errorf("PRAVO_PLAN=%q: want memo, full or check", v)
	}
	return nil
}

// planMemo says where the plans of root keep their memo: a file for the root
// and its environment in the cache directory of the user, and a key in the
// configuration directory, which is made when there is none. It is nil when
// PRAVO_PLAN=full, and when either cannot be had: the plan evaluates every
// resource then.
func planMemo(e env.Env, root *config.Root) *engine.PlanMemo {
	if planMode == "full" || root == nil {
		return nil
	}
	cache, ok := e.UserCacheDir()
	if !ok {
		return nil
	}
	conf, err := protect.ConfigDir(e)
	if err != nil {
		return nil
	}
	key, err := planMemoKey(filepath.Join(conf, "plan-memo.key"))
	if err != nil {
		return nil
	}
	id := sha256.Sum256([]byte(root.Dir + "\x00" + root.Environment))
	return &engine.PlanMemo{
		Path:  filepath.Join(cache, "pravo", "plan-memo", hex.EncodeToString(id[:16])+".json"),
		Key:   key,
		Check: planMode == "check",
	}
}

// planMemoKey reads the key at path, and makes one when there is none: 32
// random bytes, written in hex to a file that only the user can read.
func planMemoKey(path string) ([]byte, error) {
	read := func() ([]byte, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		key, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s does not hold a key", path)
		}
		return key, nil
	}
	if key, err := read(); err == nil || !os.IsNotExist(err) {
		return key, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		// Another command made it in the meantime.
		return read()
	}
	if err != nil {
		return nil, err
	}
	_, werr := f.WriteString(hex.EncodeToString(key) + "\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		os.Remove(path)
		return nil, fmt.Errorf("writing %s", path)
	}
	return key, nil
}
