package main

import (
	"context"
	"net"
	"reflect"
	"testing"
)

func TestDNSServersParsesTheAndroidList(t *testing.T) {
	got := dnsServers(" 192.168.1.1, 2001:4860:4860::8888,,not-an-ip, fe80::1%wlan0 ,fe80::x%wlan0")
	want := []string{"192.168.1.1:53", "[2001:4860:4860::8888]:53", "[fe80::1%wlan0]:53"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dnsServers = %q, want %q", got, want)
	}
	if s := dnsServers(""); len(s) != 0 {
		t.Fatalf("empty MPC_DNS gave %q", s)
	}
}

// A real lookup through a tiny DNS server on 127.0.0.1, reached only through MPC_DNS-style servers, the first of which
// refuses (a closed UDP port): the resolver must move on to the second and answer from it.
func TestDNSDialSkipsADeadServerAndResolves(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no UDP on loopback:", err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := buf[:n]
			if len(q) < 12 {
				continue
			}
			// Answer every question with 10.1.2.3: header with QR+RD+RA, the question copied back, one A record.
			end := 12
			for end < len(q) && q[end] != 0 {
				end += int(q[end]) + 1
			}
			end += 5 // the zero label, QTYPE, QCLASS
			if end > len(q) {
				continue
			}
			qtype := uint16(q[end-4])<<8 | uint16(q[end-3])
			resp := append([]byte{q[0], q[1], 0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0}, q[12:end]...)
			if qtype == 1 {
				resp[7] = 1
				resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 10, 1, 2, 3)
			}
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	dead, err := net.ListenPacket("udp", "127.0.0.1:0") // a UDP port that is closed again, so a query to it is refused
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.LocalAddr().String()
	dead.Close()
	r := &net.Resolver{PreferGo: true, Dial: dnsDial([]string{deadAddr, pc.LocalAddr().String()})}
	ctx, cancel := context.WithTimeout(context.Background(), 20e9)
	defer cancel()
	ips, err := r.LookupIP(ctx, "ip4", "catalog.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || ips[0].String() != "10.1.2.3" {
		t.Fatalf("got %v, want [10.1.2.3]", ips)
	}
}
