package push

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The sender's phone preference outlives its GUI lease. Read the atomically
// saved settings before queueing, claiming or sending, including after a
// worker restart. A transient read error preserves the last valid preference.
// Call with s.mu held.
func (s *service) refreshPhonePreferenceLocked() {
	var settings struct {
		HidePhoneNotifications bool `json:"hidePhoneNotifications"`
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "settings.json"))
	if !errors.Is(err, os.ErrNotExist) {
		if err != nil || len(data) > 128<<10 || json.Unmarshal(data, &settings) != nil {
			return
		}
	}
	s.phoneDisabled = settings.HidePhoneNotifications
	if !s.phoneDisabled {
		return
	}
	// Discard phone deliveries already queued, including retries. Re-enabling
	// must not replay them; desktop claims remain independent of this switch.
	changed := len(s.state.Pending) > 0
	for key := range s.state.Pending {
		s.removeLocked(key)
	}
	if changed && s.saveLocked() != nil {
		s.lastError = "无法保存通知状态"
	}
}
