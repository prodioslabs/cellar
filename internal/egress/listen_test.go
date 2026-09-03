package egress

import (
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestListenDNSRetriesUntilAddrReady(t *testing.T) {
	t.Cleanup(func() {
		listenUDP = net.ListenUDP
		listenTCP = net.Listen
		dnsBindAttempts = 20
		dnsBindRetry = 50 * time.Millisecond
	})
	dnsBindAttempts = 5
	dnsBindRetry = time.Millisecond

	var udpCalls atomic.Int32
	listenUDP = func(network string, laddr *net.UDPAddr) (*net.UDPConn, error) {
		n := udpCalls.Add(1)
		if n < 3 {
			return nil, syscall.EADDRNOTAVAIL
		}
		return net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	}
	listenTCP = func(network, address string) (net.Listener, error) {
		return net.Listen("tcp", "127.0.0.1:0")
	}

	udp, tcp, err := listenDNS("172.30.0.2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = udp.Close()
		_ = tcp.Close()
	})
	if got := udpCalls.Load(); got != 3 {
		t.Fatalf("udp calls=%d want 3", got)
	}
}

func TestCatchAllSpecsAvoidsInvertedMultiport(t *testing.T) {
	specs := catchAllSpecs("172.30.0.2", "172.30.0.3")
	if len(specs) != 5 {
		t.Fatalf("specs=%d want 5 (3 except + to-leg + routed)", len(specs))
	}
	for i, spec := range specs {
		joined := strings.Join(spec, " ")
		if strings.Contains(joined, "multiport") {
			t.Fatalf("spec[%d] uses multiport (nftables cannot invert it): %s", i, joined)
		}
		if strings.Contains(joined, "!") && strings.Contains(joined, "dport") {
			t.Fatalf("spec[%d] inverts a dport match: %s", i, joined)
		}
	}
	if got := strings.Join(specs[0], " "); !strings.Contains(got, "--dport 53") || !strings.Contains(got, "-j RETURN") {
		t.Fatalf("spec[0]=%s want RETURN for :53", got)
	}
	if got := strings.Join(specs[3], " "); !strings.Contains(got, "REDIRECT") || !strings.Contains(got, "--to-ports") {
		t.Fatalf("spec[3]=%s want catch-all REDIRECT", got)
	}
}

func TestListenDNSGivesUp(t *testing.T) {
	t.Cleanup(func() {
		listenUDP = net.ListenUDP
		listenTCP = net.Listen
		dnsBindAttempts = 20
		dnsBindRetry = 50 * time.Millisecond
	})
	dnsBindAttempts = 3
	dnsBindRetry = time.Millisecond

	listenUDP = func(network string, laddr *net.UDPAddr) (*net.UDPConn, error) {
		return nil, syscall.EADDRNOTAVAIL
	}
	listenTCP = func(network, address string) (net.Listener, error) {
		t.Fatal("tcp listen should not be called when udp fails")
		return nil, errors.New("unused")
	}

	udp, tcp, err := listenDNS("172.30.0.2")
	if err == nil {
		_ = udp.Close()
		_ = tcp.Close()
		t.Fatal("expected error")
	}
	if udp != nil || tcp != nil {
		t.Fatalf("expected nil listeners, got udp=%v tcp=%v", udp, tcp)
	}
}
