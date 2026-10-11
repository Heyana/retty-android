package push

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"retty/internal/atomicfile"
	"retty/internal/rex"
)

func TestPhonePreferenceSurvivesGUIExitAndWorkerRestart(t *testing.T) {
	s, provider, h, p, registration := newFixture(t)
	now := time.Now()
	p = complete(s, h, p)
	if len(s.state.Pending) != 1 {
		t.Fatal("fixture did not queue a phone notification")
	}
	set := func(data string) {
		t.Helper()
		if err := atomicfile.Write(filepath.Join(s.dir, "settings.json"), []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	set(`{"hidePhoneNotifications":true}`)
	// No GUI activity/lease is needed to honor the saved preference.
	s.deliver(context.Background(), now.Add(4*time.Second))
	if len(provider.sent) != 0 || len(s.state.Pending) != 0 || s.state.Devices[registration.ID].Token == "" {
		t.Fatal("disabling did not discard pending phone delivery or erased pairing")
	}
	if route, err := s.claim(fixtureNotice(s, h, p), now); err != nil || route != RouteDesktop {
		t.Fatal("phone disable also blocked desktop delivery", route, err)
	}
	p = complete(s, h, p)
	reloaded, err := newService(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.provider = provider
	reloaded.observe(h, []rex.SessionInfo{p}, now.Add(5*time.Second))
	p = complete(reloaded, h, p)
	reloaded.deliver(context.Background(), now.Add(9*time.Second))
	if len(provider.sent) != 0 || len(reloaded.state.Pending) != 0 {
		t.Fatal("worker restart lost the disabled preference")
	}
	// Re-enabling only resumes future events, and desktop disable does not
	// disable the independent phone channel.
	set(`{"hidePhoneNotifications":false,"hideDesktopNotifications":true}`)
	reloaded.observe(h, []rex.SessionInfo{p}, now.Add(10*time.Second))
	reloaded.deliver(context.Background(), now.Add(14*time.Second))
	if len(provider.sent) != 0 {
		t.Fatal("re-enable replayed an event from the disabled interval")
	}
	p = complete(reloaded, h, p)
	reloaded.deliver(context.Background(), now.Add(18*time.Second))
	if len(provider.sent) != 1 {
		t.Fatal("future phone event did not resume independently of desktop preference")
	}
}

func TestDisablingPhoneCancelsPendingSend(t *testing.T) {
	s, provider, h, p, _ := newFixture(t)
	complete(s, h, p)
	provider.started = make(chan struct{})
	provider.resume = make(chan struct{})
	defer close(provider.resume)
	done := make(chan struct{})
	go func() {
		s.deliver(context.Background(), time.Now().Add(4*time.Second))
		close(done)
	}()
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture send did not start")
	}
	if err := atomicfile.Write(filepath.Join(s.dir, "settings.json"), []byte(`{"hidePhoneNotifications":true}`)); err != nil {
		t.Fatal(err)
	}
	s.status() // The GUI's status query refreshes the saved switch at once.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disabled phone send was not canceled")
	}
	if len(s.state.Pending) != 0 || s.state.Sent != 0 {
		t.Fatal("canceled send was retained or recorded as delivered")
	}
}
