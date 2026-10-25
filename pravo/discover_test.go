package pravo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
)

func TestDiscoverWithoutDiscoveriesRunsNone(t *testing.T) {
	ws := open(t, specDir(t, greeting), Env{}, StateWrite)
	res, err := ws.Discover(context.Background(), DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Chosen) != 0 || res.Failed != 0 || res.Report == nil {
		t.Errorf("result = %+v", res)
	}
}

func TestDiscoverRefusesAnUnknownNameWithTheCodeOfInvalid(t *testing.T) {
	ws := open(t, specDir(t, greeting), Env{}, StateWrite)
	_, err := ws.Discover(context.Background(), DiscoverOptions{Names: []string{"vms"}})
	var coded *engine.CodedError
	if !errors.As(err, &coded) || coded.Code != engine.ExitInvalid || !strings.Contains(err.Error(), "there is no discovery") {
		t.Errorf("err = %v", err)
	}
	if _, err := ws.Discover(context.Background(), DiscoverOptions{Names: []string{"vms"}, All: true}); err == nil {
		t.Error("All with names is accepted")
	}
}
