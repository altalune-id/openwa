package fakes

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
)

// Groups is a scripted chat.Groups.
type Groups struct {
	mu        sync.Mutex
	Joined    []chat.GroupInfo
	Infos     map[string]chat.GroupInfo
	JoinJID   string
	Err       error
	Left      []string
	JoinCalls int
}

var _ chat.Groups = (*Groups)(nil)

func (f *Groups) List(context.Context, uuid.UUID) ([]chat.GroupInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]chat.GroupInfo(nil), f.Joined...), f.Err
}

func (f *Groups) Info(_ context.Context, _ uuid.UUID, jid string) (chat.GroupInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return chat.GroupInfo{}, f.Err
	}
	return f.Infos[jid], nil
}

func (f *Groups) Join(context.Context, uuid.UUID, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.JoinCalls++
	return f.JoinJID, f.Err
}

func (f *Groups) Leave(_ context.Context, _ uuid.UUID, jid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Left = append(f.Left, jid)
	return f.Err
}
