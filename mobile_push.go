package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/egoist/mygo"
	"retty/internal/rex"
	"sort"
	"strings"
	"time"
)

// APNs tokens are refreshed from the OS on every launch and retained only in
// memory on iOS. The installation ID and user preference live in Keychain.
func (m *mobileApp) setupPush() {
	mygo.App.OnPushToken(func(token string) {
		m.pushToken = token
		m.pushError = ""
		m.refreshPushSnapshot()
		m.syncPushRegistration()
	})
	mygo.App.OnPushRegistrationError(func(error) {
		m.pushError = "无法注册后台通知，请稍后重试"
		m.invalidate()
	})
	mygo.App.SetNotificationPresentationHandler(m.presentNotification)
	if m.store == nil || m.storage == nil {
		return
	}
	m.storage <- func() {
		value, err := m.store.Get("notification-device")
		if errors.Is(err, mygo.ErrSecretNotFound) {
			var identity [16]byte
			if _, err = rand.Read(identity[:]); err == nil {
				value = []byte(hex.EncodeToString(identity[:]))
				err = m.store.Set("notification-device", value)
			}
		}
		disabled := false
		var receipts map[string]time.Time
		if saved, e := m.store.Get("notification-receipts"); e == nil {
			_ = json.Unmarshal(saved, &receipts)
		}
		if preference, e := m.store.Get("notifications-enabled"); e == nil {
			disabled = string(preference) == "0"
		}
		mygo.RunOnMain(func() {
			m.mergeNoticeReceipts(receipts)
			if err != nil || len(value) != 32 {
				m.pushError = "无法保存通知设备，请重试"
				m.invalidate()
				return
			}
			m.pushDeviceID = string(value)
			m.pushDisabled = disabled
			m.refreshPushSnapshot()
			m.syncPushRegistration()
			m.registerSystemPush()
		})
	}
}
func (m *mobileApp) registerSystemPush() {
	if m.pushDisabled {
		m.refreshPushSnapshot()
		m.syncPushRegistration()
		return
	}
	// Readiness precedes UIKit activation. Permission prompts require an active
	// app; activation also retries after a launch alert or returning from Settings.
	if m.pushDeviceID == "" || m.pushRequesting || mygo.App.Lifecycle() != mygo.LifecycleActive {
		return
	}
	m.pushRequesting = true
	go func() {
		status, err := mygo.Permissions.Request(mygo.PermissionNotifications)
		mygo.RunOnMain(func() {
			m.pushRequesting = false
			if m.pushDisabled {
				return
			}
			m.notificationDenied = status == mygo.PermissionDenied
			if err != nil {
				m.pushError = "无法开启通知，请稍后重试"
				m.invalidate()
				return
			}
			if m.notificationDenied {
				m.pushError = "请在系统设置中允许通知"
				m.refreshPushSnapshot()
				m.syncPushRegistration()
				m.invalidate()
				return
			}
			m.pushError = ""
			if err := mygo.App.RegisterPushNotifications(); err != nil {
				m.pushError = "无法注册后台通知"
				m.invalidate()
			}
		})
	}()
}
func (m *mobileApp) setPushEnabled(enabled bool) {
	m.pushDisabled = !enabled
	m.save("notifications-enabled", keychainBool(enabled), func() { m.pushError = "无法保存通知设置"; m.invalidate() })
	m.refreshPushSnapshot()
	m.syncPushRegistration()
	if enabled {
		m.registerSystemPush()
	}
	m.invalidate()
}
func (m *mobileApp) taskNoticeID(session rex.SessionInfo) string {
	return inboxNoticeID(m.hello, m.desktopKey(), session)
}
func (m *mobileApp) rememberNotice(id string) bool {
	if !strings.HasPrefix(id, "retty-agent-") || len(id) > 64 {
		return false
	}
	if m.noticeReceipts == nil {
		m.noticeReceipts = map[string]time.Time{}
	}
	m.trimNoticeReceipts()
	if _, seen := m.noticeReceipts[id]; seen {
		return false
	}
	m.noticeReceipts[id] = time.Now()
	m.trimNoticeReceipts()
	m.persistNoticeReceipts()
	return true
}
func (m *mobileApp) trimNoticeReceipts() {
	for id, at := range m.noticeReceipts {
		if time.Since(at) > 7*24*time.Hour {
			delete(m.noticeReceipts, id)
		}
	}
	trimReceipts(m.noticeReceipts, 128)
}
func (m *mobileApp) mergeNoticeReceipts(saved map[string]time.Time) {
	if m.noticeReceipts == nil {
		m.noticeReceipts = map[string]time.Time{}
	}
	for id, at := range saved {
		if current, exists := m.noticeReceipts[id]; !exists || at.After(current) {
			m.noticeReceipts[id] = at
		}
	}
	m.trimNoticeReceipts()
	m.noticeReceiptsLoaded = true
	m.persistNoticeReceipts()
}
func (m *mobileApp) persistNoticeReceipts() {
	if m.noticeReceiptsLoaded {
		data, _ := json.Marshal(m.noticeReceipts)
		m.save("notification-receipts", data, nil)
	}
}
func (m *mobileApp) refreshPushSnapshot() {
	info := mobileDevice()
	// The Keychain installation ID also identifies reconnects without APNs.
	info.ID = m.pushDeviceID
	if m.pushDeviceID != "" && (m.pushToken != "" || m.pushDisabled || m.notificationDenied) {
		ids := make([]string, 0, len(m.noticeReceipts))
		for id := range m.noticeReceipts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		info.Push = &rex.PushRegistration{ID: m.pushDeviceID, Token: m.pushToken, Disabled: m.pushDisabled || m.notificationDenied, Receipts: ids}
	}
	m.pushSnapshot.Store(&info)
}
func (m *mobileApp) syncPushRegistration() {
	if m.client == nil {
		return
	}
	info := m.pushSnapshot.Load()
	if info == nil || info.ID == "" && info.Push == nil {
		return
	}
	client := m.client
	copyInfo := *info
	go func() { client.HelloFrom(copyInfo) }()
}
func (m *mobileApp) presentNotification(event mygo.NotificationEvent) mygo.NotificationPresentation {
	if event.Source != mygo.NotificationRemote {
		return mygo.PresentNotificationDefault
	}
	id := event.Data["event"]
	desktop, sid := event.Data["desktop"], event.Data["session"]
	if id == "" || desktop == "" || sid == "" {
		return 0
	}
	if !m.rememberNotice(id) {
		return 0
	}
	m.refreshPushSnapshot()
	m.syncPushRegistration()
	// Foreground delivery only records the receipt. APNs owns background
	// notifications; active mobile pages update session status without overlays.
	return 0
}
