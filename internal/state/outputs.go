package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ReadOutputs reads the recorded outputs of the state kept in b. It reads the
// one object outputs.json and nothing else, and creates nothing.
func ReadOutputs(ctx context.Context, b Backend) (map[string]json.RawMessage, error) {
	data, err := b.Get(ctx, "outputs.json")
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%s has no recorded outputs; apply that root first", b.Name())
	}
	if err != nil {
		return nil, err
	}
	if _, sealed := asSealedOutputs(data); sealed {
		return nil, fmt.Errorf("the outputs of %s are sealed to the readers of that state, and remote_outputs reads them without a key: %w", b.Name(), ErrNotReader)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("reading outputs of %s: %w", b.Name(), err)
	}
	return out, nil
}
