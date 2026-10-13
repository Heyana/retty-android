package remote

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/net/netmon"
)

func TestDiscoveryNetworkChangeRestartsWarmRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := &Quality{}
	wake := make(chan struct{}, 1)
	calls := make(chan int, 4)
	n := 0
	q.bindProbe(func(context.Context) (PathSample, error) {
		n++
		calls <- n
		// The old network was direct. On the new network the first probe
		// is relayed, then the warm retry establishes a direct path.
		return PathSample{At: time.Now(), Direct: n != 2}, nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		discoverPath(ctx, q, time.Millisecond, time.Hour, wake)
	}()
	for want := 1; want <= 3; want++ {
		select {
		case got := <-calls:
			if got != want {
				t.Fatalf("probe %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("network change did not trigger probe %d", want)
		}
		if want == 1 {
			wake <- struct{}{}
		}
	}
	cancel()
	<-done
	if !q.Snapshot(time.Now()).Path.Direct {
		t.Fatal("new network stayed relayed")
	}
}

func TestPathDiscoveryPrewarmsAndHandsOffMeasurements(t *testing.T) {
	var calls atomic.Int32
	d := newPathDiscovery(func(context.Context) (PathSample, error) {
		n := calls.Add(1)
		return PathSample{At: time.Now(), Direct: true, Latency: time.Duration(n) * time.Millisecond}, nil
	})
	d.start()
	defer func() {
		d.cancel()
		select {
		case <-d.done:
		case <-time.After(3 * time.Second):
			t.Error("parked tunnel discovery survived close")
		}
	}()
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatal("discovery did not publish its sample")
			}
			time.Sleep(time.Millisecond)
		}
	}
	// The preview has no quality subscriber, but it must still discover
	// and retain a direct path before the user connects.
	wait(func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return !d.last.At.IsZero()
	})
	d.start() // A handoff must not create a second worker.
	q := &Quality{}
	unbind := d.bind(q)
	defer unbind()
	if p := q.Snapshot(time.Now()).Path; !p.Direct || p.Latency != time.Millisecond {
		t.Fatal("connection did not inherit prewarmed measurements", p)
	}
	// Rebinding the same metrics must reject the old owner's cleanup and
	// continue to receive background samples.
	unbindNew := d.bind(q)
	defer unbindNew()
	unbind()
	d.request()
	wait(func() bool { return q.Snapshot(time.Now()).Path.Latency == 2*time.Millisecond })
	if calls.Load() != 2 {
		t.Fatal("handoff started duplicate discovery workers", calls.Load())
	}
}

func TestPathNetworkChangeFilter(t *testing.T) {
	for _, change := range []*netmon.ChangeDelta{nil, {}, {IsInitialState: true, RebindLikelyRequired: true}} {
		if pathNetworkChanged(change) {
			t.Fatal("unrelated or initial network update woke discovery")
		}
	}
	for _, change := range []*netmon.ChangeDelta{{RebindLikelyRequired: true}, {JumpDuration: time.Minute}} {
		if !pathNetworkChanged(change) {
			t.Fatal("network change or wake did not refresh discovery")
		}
	}
}

func TestDiscoveryRetriesRelayAndStopsWithTunnel(t *testing.T) {
	q := &Quality{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	direct := make(chan struct{})
	q.bindProbe(func(context.Context) (PathSample, error) {
		n := calls.Add(1)
		if n == 1 {
			return PathSample{}, errors.New("temporary discovery failure")
		}
		if n == 3 {
			close(direct)
		}
		return PathSample{At: time.Now(), Direct: n >= 3}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); discoverPath(ctx, q, time.Millisecond, time.Hour, nil) }()
	select {
	case <-direct:
	case <-time.After(time.Second):
		t.Fatal("did not retry failed and relayed probes")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("discovery survived tunnel close")
	}
	if calls.Load() != 3 || !q.Snapshot(time.Now()).Path.Direct {
		t.Fatal("unexpected discovery result", calls.Load())
	}
}

func TestPathProbeCancellationReleasesWaitingProbes(t *testing.T) {
	lifetime, closeTunnel := context.WithCancel(context.Background())
	defer closeTunnel()
	var calls atomic.Int32
	started := make(chan struct{})
	probe := serializedPathProbe(lifetime, func(ctx context.Context) (PathSample, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return PathSample{}, ctx.Err()
	})
	done := make(chan error, 2)
	go func() { _, err := probe(context.Background()); done <- err }()
	<-started
	go func() { _, err := probe(context.Background()); done <- err }()
	closeTunnel()
	for range 2 {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("probe did not inherit tunnel cancellation", err)
			}
		case <-time.After(time.Second):
			t.Fatal("probe blocked tunnel cleanup")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent probe entered disposed tunnel", calls.Load())
	}
}

func TestPathProbeCallerCancellationDoesNotStopDiscovery(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	probe := serializedPathProbe(lifetime, func(ctx context.Context) (PathSample, error) {
		calls++
		return PathSample{At: time.Now(), Direct: true}, nil
	})
	popup, closePopup := context.WithCancel(context.Background())
	closePopup()
	if _, err := probe(popup); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled popup entered probe", err)
	}
	if p, err := probe(context.Background()); err != nil || !p.Direct || calls != 1 {
		t.Fatal("popup cancellation stopped tunnel discovery", p, err, calls)
	}
}
