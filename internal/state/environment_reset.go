package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
)

// ReplaceEnvironmentRecord is the part of recovery that follows ReplaceRoles. When the record of
// the environment pins the roles, the new first version of the roles is not the one it pins, and
// the admins it names may be gone. The versions of the record are thrown away, and the newest
// one is stored again as version 1, signed by this store's key, with the pin set to pin. It does
// nothing, and reports false, when the state has no record or the record pins no roles.
func (s *Store) ReplaceEnvironmentRecord(ctx context.Context, pin string) (bool, error) {
	keys, err := s.b.List(ctx, environmentDir)
	if err != nil {
		return false, err
	}
	newest, newestKey := 0, ""
	for _, k := range keys {
		if m := environmentKeyRe.FindStringSubmatch(k); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > newest {
				newest, newestKey = n, k
			}
		}
	}
	if newest == 0 {
		return false, nil
	}
	data, err := s.b.Get(ctx, newestKey)
	if err != nil {
		return false, err
	}
	var rec EnvironmentRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return false, err
	}
	if rec.RolesPin == "" {
		return false, nil
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := s.b.Delete(ctx, k); err != nil && !errors.Is(err, ErrNotFound) {
			return false, Advise(err)
		}
	}
	rec.RolesPin, rec.Seq, rec.Signature = pin, 0, nil
	return true, s.PutEnvironmentRecord(ctx, 1, rec)
}
