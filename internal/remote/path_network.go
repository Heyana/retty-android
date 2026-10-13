package remote

import (
	"sync"

	"tailscale.com/net/netmon"
	"tailscale.com/util/eventbus"
)

// Tailcat already watches interface changes to advertise UDP endpoints. This
// shared observer wakes Retty's discovery retries without rebuilding tunnels.
// It exists only while there are active or parked tunnels.
var pathNetwork struct {
	sync.Mutex
	monitor *netmon.Monitor
	users   int
}

func pathNetworkChanged(change *netmon.ChangeDelta) bool {
	return change != nil && !change.IsInitialState && (change.RebindLikelyRequired || change.TimeJumped())
}

func watchPathNetwork(wake func()) func() {
	pathNetwork.Lock()
	defer pathNetwork.Unlock()
	if pathNetwork.monitor == nil {
		monitor, err := netmon.New(eventbus.New(), quiet)
		if err != nil {
			// Discovery still has its bounded periodic retries.
			return func() {}
		}
		pathNetwork.monitor = monitor
		monitor.Start()
	}
	monitor := pathNetwork.monitor
	pathNetwork.users++
	unregister := monitor.RegisterChangeCallback(func(change *netmon.ChangeDelta) {
		if pathNetworkChanged(change) {
			wake()
		}
	})
	return sync.OnceFunc(func() {
		unregister()
		pathNetwork.Lock()
		pathNetwork.users--
		last := pathNetwork.users == 0
		if last {
			pathNetwork.monitor = nil
		}
		pathNetwork.Unlock()
		if last {
			monitor.Close()
		}
	})
}
