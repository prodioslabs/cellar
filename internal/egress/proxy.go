package egress

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

// Proxy is a per-node transparent TCP (+ DNS UDP) egress proxy.
type Proxy struct {
	mu       sync.RWMutex
	policies map[string]*Evaluator
	tcpLis   net.Listener
	udpConn  *net.UDPConn
	TCPPort  int
	UDPPort  int
}

// NewProxy creates an unstarted proxy.
func NewProxy() *Proxy {
	return &Proxy{policies: make(map[string]*Evaluator)}
}

// SetPolicy registers or replaces policy for a sandbox.
func (p *Proxy) SetPolicy(sandboxID string, policy sandbox.NetworkPolicy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policies[sandboxID] = NewEvaluator(policy)
}

// RemovePolicy drops a sandbox policy.
func (p *Proxy) RemovePolicy(sandboxID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.policies, sandboxID)
}

// HasPolicy reports whether a sandbox policy is registered.
func (p *Proxy) HasPolicy(sandboxID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.policies[sandboxID]
	return ok
}

// Start listens on ephemeral TCP/UDP ports for redirected traffic.
func (p *Proxy) Start(ctx context.Context) error {
	tcpLis, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return err
	}
	udpAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		_ = tcpLis.Close()
		return err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		_ = tcpLis.Close()
		return err
	}
	p.tcpLis = tcpLis
	p.udpConn = udpConn
	p.TCPPort = tcpLis.Addr().(*net.TCPAddr).Port
	p.UDPPort = udpConn.LocalAddr().(*net.UDPAddr).Port

	go p.serveTCP(ctx)
	go p.serveUDP(ctx)
	log.Printf("egress proxy listening tcp=:%d udp=:%d", p.TCPPort, p.UDPPort)
	return nil
}

// Close stops listeners.
func (p *Proxy) Close() error {
	var err error
	if p.tcpLis != nil {
		err = p.tcpLis.Close()
	}
	if p.udpConn != nil {
		_ = p.udpConn.Close()
	}
	return err
}

func (p *Proxy) serveTCP(ctx context.Context) {
	for {
		conn, err := p.tcpLis.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		go p.handleTCP(conn)
	}
}

func (p *Proxy) handleTCP(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	orig, err := originalDST(conn)
	if err != nil {
		log.Printf("egress: original dst: %v", err)
		return
	}
	host, port, err := ParseHostPort(orig)
	if err != nil {
		return
	}
	if !p.allowAny(host, port) {
		log.Printf("egress deny tcp %s", orig)
		return
	}
	dst, err := net.DialTimeout("tcp", orig, 15*time.Second)
	if err != nil {
		return
	}
	defer dst.Close()
	_ = conn.SetDeadline(time.Time{})
	_ = dst.SetDeadline(time.Time{})
	proxyCopy(conn, dst)
}

func (p *Proxy) allowAny(host string, port uint32) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.policies) == 0 {
		return false
	}
	hasAllowlist := false
	anyAllow := false
	for _, ev := range p.policies {
		if ev.policy.Mode == sandbox.NetworkAllowlist {
			hasAllowlist = true
			if ev.AllowConnect(host, port) == Allow {
				anyAllow = true
			}
		}
	}
	if hasAllowlist {
		return anyAllow
	}
	for _, ev := range p.policies {
		if ev.AllowConnect(host, port) == Deny {
			return false
		}
	}
	return true
}

func (p *Proxy) allowDNSAny(name string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.policies) == 0 {
		return false
	}
	hasAllowlist := false
	anyAllow := false
	for _, ev := range p.policies {
		mode := ev.policy.DNS.Mode
		if mode == "" {
			mode = sandbox.DNSMode(ev.policy.Mode)
		}
		if mode == sandbox.DNSAllowlist {
			hasAllowlist = true
			if ev.AllowDNS(name) == Allow {
				anyAllow = true
			}
		}
	}
	if hasAllowlist {
		return anyAllow
	}
	for _, ev := range p.policies {
		if ev.AllowDNS(name) == Deny {
			return false
		}
	}
	return true
}

func proxyCopy(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
}

func originalDST(conn net.Conn) (string, error) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return "", fmt.Errorf("not tcp")
	}
	file, err := tcp.File()
	if err != nil {
		return "", err
	}
	defer file.Close()
	fd := int(file.Fd())

	const soOriginalDst = 80
	var addr [16]byte
	vallen := uint32(len(addr))
	_, _, errno := syscall.RawSyscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(syscall.IPPROTO_IP),
		uintptr(soOriginalDst),
		uintptr(unsafe.Pointer(&addr[0])),
		uintptr(unsafe.Pointer(&vallen)),
		0,
	)
	if errno != 0 {
		return "", errno
	}
	port := binary.BigEndian.Uint16(addr[2:4])
	ip := net.IPv4(addr[4], addr[5], addr[6], addr[7])
	return net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)), nil
}

func (p *Proxy) serveUDP(ctx context.Context) {
	buf := make([]byte, 1500)
	for {
		n, addr, err := p.udpConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		go p.handleDNS(append([]byte(nil), buf[:n]...), addr)
	}
}

func (p *Proxy) handleDNS(pkt []byte, client *net.UDPAddr) {
	name, err := parseDNSQName(pkt)
	if err != nil || name == "" {
		return
	}
	if !p.allowDNSAny(name) {
		log.Printf("egress deny dns %s", name)
		return
	}
	resp, err := net.LookupIP(name)
	if err != nil || len(resp) == 0 {
		return
	}
	var a net.IP
	for _, ip := range resp {
		if v4 := ip.To4(); v4 != nil {
			a = v4
			break
		}
	}
	if a == nil {
		return
	}
	out, err := buildDNSAResponse(pkt, a)
	if err != nil {
		return
	}
	_, _ = p.udpConn.WriteToUDP(out, client)
}
