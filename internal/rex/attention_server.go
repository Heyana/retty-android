//go:build darwin || linux

package rex

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"retty/internal/atomicfile"
)

func (s *Server) loadAttention(dir string) error {
	s.attentionPath = filepath.Join(dir, "attention-seen.json")
	s.attentionSeen = map[string]string{}
	f, err := os.Open(s.attentionPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, 256<<10)).Decode(&s.attentionSeen)
}

func (s *Server) markAttention(sid, id string) error {
	if s.attentionPath == "" {
		return errors.New("attention receipts unavailable")
	}
	ss, err := s.session(sid)
	if err != nil {
		return err
	}
	info := ss.info()
	if !AttentionState(info) || !strings.HasPrefix(id, "retty-agent-") || id != AgentNoticeID(s.hostID, info) {
		return errors.New("attention event changed")
	}
	s.attentionMu.Lock()
	defer s.attentionMu.Unlock()
	if s.attentionSeen[sid] == id {
		return nil
	}
	next := make(map[string]string)
	// Keep only receipts for live sessions; a completed session cannot be
	// resurrected by a stale acknowledgement or grow the file indefinitely.
	s.mu.Lock()
	for key, value := range s.attentionSeen {
		if s.sessions[key] != nil {
			next[key] = value
		}
	}
	s.mu.Unlock()
	next[sid] = id
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.attentionPath, data); err != nil {
		return err
	}
	s.attentionSeen = next
	return nil
}
