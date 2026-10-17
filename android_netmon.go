//go:build android && cgo

package main

// Android SDK 30 stopped letting app UIDs enumerate network interfaces with
// net.Interfaces (EACCES, surfaced as "route ip+net: netlinkrib: permission
// denied"). Tailscale's netmon calls it before the tunnel exists, so the
// connection failed with permission_denied before a packet was sent.
//
// netmon.RegisterInterfaceGetter is the seam for exactly this. Tailscale's
// own Android app registers a getter backed by the Java NetworkInterface API;
// that needs a round trip through the platform bridge, which is more than the
// tunnel needs. What netmon consumes from the list is the interface's names,
// MTU and addresses — and of those, only the outbound addresses matter here:
// they are what the relay learns as this client's endpoints.
//
// So the getter synthesizes one interface carrying the process's outbound
// IPv4 and IPv6 source addresses, the same shape tailscale.com/feature/
// androidbin uses for raw binaries on Android (that package excludes
// android+cgo builds, which is what this is, so it cannot simply be imported).
// Dialing UDP makes the kernel pick a route and a source address without
// sending a packet.

import (
	"fmt"
	"net"
	"net/netip"
	"time"

	"tailscale.com/net/netmon"
)

func init() {
	netmon.RegisterInterfaceGetter(androidInterfaces)
}

// probeTargets are never sent traffic; they only pick a route. Public DNS
// rather than TEST-NET ranges, so the choice of route is the ordinary one.
var probeTargets = []struct{ network, addr string }{
	{"udp4", "8.8.8.8:53"},
	{"udp6", "[2001:4860:4860::8888]:53"},
}

func androidInterfaces() ([]netmon.Interface, error) {
	var addrs []net.Addr
	for _, t := range probeTargets {
		if ip, ok := outboundIP(t.network, t.addr); ok {
			bits := 128
			if ip.Is4() {
				bits = 32
			}
			addrs = append(addrs, &net.IPNet{IP: ip.AsSlice(), Mask: net.CIDRMask(bits, bits)})
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("androidInterfaces: no outbound route")
	}
	return []netmon.Interface{{
		Interface: &net.Interface{
			Index: 1,
			MTU:   1500,
			Name:  "wlan0",
			Flags: net.FlagUp | net.FlagRunning,
		},
		AltAddrs: addrs,
	}}, nil
}

func outboundIP(network, addr string) (netip.Addr, bool) {
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.Dial(network, addr)
	if err != nil {
		return netip.Addr{}, false
	}
	defer c.Close()
	ua, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, false
	}
	ip, ok := netip.AddrFromSlice(ua.IP)
	if !ok {
		return netip.Addr{}, false
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsUnspecified() {
		return netip.Addr{}, false
	}
	return ip, true
}
