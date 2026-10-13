package remote

import (
	"context"
	"sync"
	"time"
)

// pathDiscovery lives with the tunnel, including while it is parked by a
// home-page preview. Connecting only subscribes to its existing measurements.
type pathDiscovery struct {
	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	probe  func(context.Context) (PathSample, error)
	once   sync.Once
	done   chan struct{}

	mu      sync.Mutex
	last    PathSample
	publish func(PathSample, error)
	epoch   uint64
}

func newPathDiscovery(probe func(context.Context) (PathSample, error)) *pathDiscovery {
	ctx, cancel := context.WithCancel(context.Background())
	d := &pathDiscovery{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	d.probe = serializedPathProbe(ctx, func(ctx context.Context) (PathSample, error) {
		p, err := probe(ctx)
		d.mu.Lock()
		defer d.mu.Unlock()
		d.last = PathSample{}
		if err == nil {
			d.last = p
		}
		if d.publish != nil {
			d.publish(p, err)
		}
		return p, err
	})
	return d
}

func (d *pathDiscovery) start() {
	d.once.Do(func() {
		go func() {
			defer close(d.done)
			if d.ctx.Err() != nil {
				return
			}
			unwatch := watchPathNetwork(d.request)
			defer unwatch()
			q := &Quality{}
			q.bindProbe(d.probe)
			discoverPath(d.ctx, q, 2*time.Second, 30*time.Second, d.wake)
		}()
	})
}

// Coalesce network notifications and redials; one worker owns the retry loop.
func (d *pathDiscovery) request() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *pathDiscovery) bind(q *Quality) func() {
	d.mu.Lock()
	d.epoch++
	epoch := d.epoch
	var unbind func()
	d.publish, unbind = q.bindPathProbe(d.probe, d.last)
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		if d.epoch == epoch {
			d.publish = nil
		}
		d.mu.Unlock()
		unbind()
	}
}

func discoverPath(ctx context.Context, q *Quality, warm, steady time.Duration, wake <-chan struct{}) {
	for attempt := 0; ctx.Err() == nil; attempt++ {
		probe, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := q.Probe(probe)
		cancel()
		delay := steady
		if attempt < 4 && (err != nil || !q.Snapshot(time.Now()).Path.Direct) {
			delay = warm << attempt
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-wake:
			timer.Stop()
			// A new network gets the short retry sequence even if the old
			// network was already direct or had exhausted its retries.
			attempt = -1
		}
	}
}

// The popover and background discovery share one ping at a time. Waiting and
// in-flight probes both stop when the owning tunnel is disposed.
func serializedPathProbe(lifetime context.Context, probe func(context.Context) (PathSample, error)) func(context.Context) (PathSample, error) {
	gate := make(chan struct{}, 1)
	return func(ctx context.Context) (PathSample, error) {
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return PathSample{}, ctx.Err()
		}
		if lifetime.Err() != nil {
			return PathSample{}, lifetime.Err()
		}
		if ctx.Err() != nil {
			return PathSample{}, ctx.Err()
		}
		return probe(ctx)
	}
}
