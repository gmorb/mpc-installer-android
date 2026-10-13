package main

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Android has no /etc/resolv.conf, so Go's own resolver (all this app has: it is built without cgo) falls back to a DNS
// server on 127.0.0.1 that is not there, and the catalog download fails with "no such host". The Android app passes the
// phone's current DNS servers in MPC_DNS (comma-separated); with that set and no resolv.conf, lookups go to those
// servers. Desktop systems have a resolv.conf (or do not use Go's resolver) and are left alone. The MPC itself is reached
// by IP address, so this only matters for the catalog and plugin downloads.

const dnsEnv = "MPC_DNS"

// dnsServers parses MPC_DNS into host:53 addresses, dropping empty and malformed entries.
func dnsServers(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if i := strings.IndexByte(s, '%'); i >= 0 { // link-local zone ("fe80::1%wlan0"): keep it, the dialer understands it
			if net.ParseIP(s[:i]) == nil {
				continue
			}
		} else if net.ParseIP(s) == nil {
			continue
		}
		out = append(out, net.JoinHostPort(s, "53"))
	}
	return out
}

// dnsDial returns a resolver Dial that ignores the address Go picked (the missing local server) and sends each attempt to
// the next server in turn. A UDP "dial" succeeds even when nobody listens, so a dead server (an IPv6 one on an IPv4-only
// Wi-Fi, say) only shows when its query times out or is refused; Go then retries, and the retry goes to the next server.
// Go makes at least four attempts per lookup without a resolv.conf (two default servers, two attempts each).
func dnsDial(servers []string) func(ctx context.Context, network, address string) (net.Conn, error) {
	var next atomic.Uint32
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		d := net.Dialer{Timeout: 4 * time.Second}
		start := int(next.Add(1)-1) % len(servers)
		var errs []error
		for i := range servers { // a server that cannot even be dialled (TCP refused, no route) is skipped at once
			c, err := d.DialContext(ctx, network, servers[(start+i)%len(servers)])
			if err == nil {
				return c, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}

func init() {
	servers := dnsServers(os.Getenv(dnsEnv))
	if len(servers) == 0 {
		return
	}
	if _, err := os.Stat("/etc/resolv.conf"); err == nil {
		return
	}
	net.DefaultResolver.PreferGo = true
	net.DefaultResolver.Dial = dnsDial(servers)
}
