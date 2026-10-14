package main

import (
	"retty/internal/remote"
	"retty/internal/rex"
	"time"
)

// locksSize tells whether an opened session takes the PTY's size: the
// setting is on, the desktop's server supports the lock, and no window
// released it for the open session.
func (m *mobileApp) locksSize() bool {
	return m.sizeLock && !m.lockLost && m.hello.Version >= 6
}

// sessionStream attaches to a session as a viewer that reflows the
// desktop's screen locally, or as the device holding the PTY's size.
func (m *mobileApp) sessionStream(sid string, lock bool) *rex.Stream {
	if lock {
		cols, rows := 0, 0
		if m.term != nil && m.selected.ID == sid {
			// A reattach keeps this view's size, which the terminal does
			// not report again while it is unchanged.
			cols, rows = m.term.Size()
		}
		m.lockOwner = newSizeLockOwner()
		return m.client.LockStreamAfter(sid, m.lockOwner, mobileDevice().Name, cols, rows, m.lockRelease).PreferScreenSync(false)
	}
	return m.client.ViewStream(sid).PreferScreenSync(false)
}

func (m *mobileApp) releaseSizeLock() bool {
	if m.stream == nil {
		return false
	}
	stream, ok := m.stream.transport().(*rex.Stream)
	if !ok || m.lockOwner == "" {
		return false
	}
	m.lockRelease = stream.CloseAndReleaseSize()
	return true
}

func (m *mobileApp) closeConnectionAfterSizeRelease() {
	client, closeTunnel, released := m.client, m.closeTunnel, m.lockRelease
	m.client, m.closeTunnel = nil, nil
	go func() {
		if released != nil {
			<-released
		}
		remote.CloseConnection(client, closeTunnel)
	}()
}

func (m *mobileApp) setSizeLock(enabled bool) {
	m.sizeLock = enabled
	m.save("size-lock", keychainBool(enabled), func() { m.error = "无法保存尺寸设置"; m.invalidate() })
	m.invalidate()
}

// reattach replaces the open session's transport after a reconnection.
// A lock a window released meanwhile is not taken back.
func (m *mobileApp) reattach(s rex.SessionInfo) {
	if !m.lockSuspended && m.locksSize() && m.stream != nil && m.term != nil && s.SizeLock != m.lockOwner {
		m.loseSizeLock(s)
		return
	}
	m.lockSuspended, m.lockSeen = false, false
	m.stream.replace(m.sessionStream(s.ID, m.locksSize()))
}

// followSizeLock switches the open session to the local reflow once a
// desktop window released its size lock. A list made before this phone's
// lock was taken must not count: the lock is lost once the phone saw it
// held, or a while after its attach took it.
func (m *mobileApp) followSizeLock(s rex.SessionInfo) {
	if m.background || m.lockSuspended || m.term == nil || s.ID != m.selected.ID || !m.locksSize() {
		return
	}
	if s.SizeLock == m.lockOwner {
		m.lockSeen = true
		return
	}
	holds, since := false, time.Time{}
	if m.stream != nil {
		if stream, ok := m.stream.transport().(*rex.Stream); ok {
			holds, since = stream.HoldsLock()
		}
	}
	if !m.lockSeen && (!holds || time.Since(since) < 3*time.Second) {
		return
	}
	m.loseSizeLock(s)
}

// loseSizeLock gives the size back to the window that took it. The phone
// leaves a session another device now sizes; a released one reopens as a
// local reflow.
func (m *mobileApp) loseSizeLock(s rex.SessionInfo) {
	m.lockLost = true
	if s.SizeLock != "" {
		m.resumeSID = ""
		m.detach()
		m.invalidate()
		return
	}
	m.openSession(s)
}

// keychainBool encodes a boolean setting as Keychain stores it.
func keychainBool(on bool) []byte {
	if on {
		return []byte("1")
	}
	return []byte("0")
}
