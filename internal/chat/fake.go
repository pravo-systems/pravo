package chat

import (
	"context"
	"errors"
	"sync"
)

// Script is a provider that answers from a list, for tests and screenshots: each
// call of Complete returns the next Response. Requests holds what it was asked.
type Script struct {
	mu       sync.Mutex
	Replies  []Response
	Requests []Request
}

func (s *Script) Complete(_ context.Context, r Request) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, r)
	if len(s.Replies) == 0 {
		return Response{}, errors.New("the script has no more replies")
	}
	next := s.Replies[0]
	s.Replies = s.Replies[1:]
	return next, nil
}
