package egress

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

// Proxy is a per-node transparent TCP (+ DNS UDP) egress proxy.
//
// Every redirected connection is attributed to exactly one sandbox by its
// source IP, and only that sandbox's policy decides the outcome. Traffic from
// an IP with no binding fails closed.
// listenerKind selects how a redirected connection is inspected. Traffic is
// split by destination port in iptables so protocol detection is never applied
// to server-first protocols such as SSH or Postgres.
type listenerKind int

const (
	kindOther listenerKind = iota // no inspection, IP and CIDR rules only
	kindHTTP                      // port 80, Host header
	kindTLS                       // port 443, TLS SNI
)

const (
	connSetupTimeout = 60 * time.Second
	peekTimeout      = 5 * time.Second
	dialTimeout      = 15 * time.Second
)

// liveConn is an established upstream connection, kept so a policy change can
// terminate what it no longer allows.
type liveConn struct {
	conn     net.Conn
	hostname string
	ip       net.IP
	port     uint32
}

type Proxy struct {
	mu         sync.RWMutex
	policies   map[string]*Evaluator
	byIP       map[string]string             // container IP -> sandbox ID
	ipOf       map[string]string             // sandbox ID -> container IP
	conns      map[string]map[*liveConn]bool // sandbox ID -> live connections
	exceptions []*net.IPNet                  // node-level carve-outs from DeniedCIDRs

	httpLis  net.Listener
	tlsLis   net.Listener
	otherLis net.Listener
	udpConn  *net.UDPConn

	HTTPPort  int
	TLSPort   int
	OtherPort int
	UDPPort   int
}

// NewProxy creates an unstarted proxy.
func NewProxy() *Proxy {
	return &Proxy{
		policies: make(map[string]*Evaluator),
		byIP:     make(map[string]string),
		ipOf:     make(map[string]string),
		conns:    make(map[string]map[*liveConn]bool),
	}
}

// SetPolicy registers or replaces policy for a sandbox and terminates any
// established connection the new policy no longer allows. Without that, a
// lock-down applied before running untrusted code would leave sockets opened
// under the old policy usable.
func (p *Proxy) SetPolicy(sandboxID string, policy sandbox.NetworkPolicy) {
	ev := NewEvaluator(policy)
	p.mu.Lock()
	p.policies[sandboxID] = ev
	var revoked []*liveConn
	for c := range p.conns[sandboxID] {
		if decision, _ := ev.AllowConnect(c.hostname, c.ip, c.port); decision != Allow {
			revoked = append(revoked, c)
		}
	}
	p.mu.Unlock()

	// Closing takes the lock again via untrackConn, so it happens after unlock.
	for _, c := range revoked {
		log.Printf("egress revoke tcp sandbox=%s dst=%s host=%q", sandboxID, c.ip, c.hostname)
		_ = c.conn.Close()
	}
}

// RemovePolicy drops a sandbox policy, its IP binding, and its connections.
func (p *Proxy) RemovePolicy(sandboxID string) {
	p.mu.Lock()
	delete(p.policies, sandboxID)
	p.unbindLocked(sandboxID)
	open := p.conns[sandboxID]
	delete(p.conns, sandboxID)
	p.mu.Unlock()

	for c := range open {
		_ = c.conn.Close()
	}
}

func (p *Proxy) trackConn(sandboxID string, c *liveConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conns[sandboxID] == nil {
		p.conns[sandboxID] = make(map[*liveConn]bool)
	}
	p.conns[sandboxID][c] = true
}

func (p *Proxy) untrackConn(sandboxID string, c *liveConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	set := p.conns[sandboxID]
	if set == nil {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(p.conns, sandboxID)
	}
}

// HasPolicy reports whether a sandbox policy is registered.
func (p *Proxy) HasPolicy(sandboxID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.policies[sandboxID]
	return ok
}

// BindSandboxIP associates a container IP with a sandbox so redirected
// connections can be attributed to it.
func (p *Proxy) BindSandboxIP(sandboxID, ip string) {
	if sandboxID == "" || ip == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if prev, ok := p.ipOf[sandboxID]; ok && prev != ip {
		if cur, ok := p.byIP[prev]; ok && cur == sandboxID {
			delete(p.byIP, prev)
		}
	}
	p.ipOf[sandboxID] = ip
	p.byIP[ip] = sandboxID
}

// UnbindSandbox drops the IP binding for a sandbox.
func (p *Proxy) UnbindSandbox(sandboxID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unbindLocked(sandboxID)
}

// SetPrivateExceptions carves node-level exceptions out of DeniedCIDRs, for
// operators whose sandboxes legitimately need a private destination.
func (p *Proxy) SetPrivateExceptions(cidrs []string) error {
	nets, err := ParseCIDRs(cidrs)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exceptions = nets
	return nil
}

// destinationDenied reports whether ip is in an always-denied range with no
// node-level exception covering it.
func (p *Proxy) destinationDenied(ip net.IP) bool {
	if !IsDenied(ip) {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !cidrsContain(p.exceptions, ip)
}

// SandboxIP returns the bound container IP for a sandbox.
func (p *Proxy) SandboxIP(sandboxID string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ip, ok := p.ipOf[sandboxID]
	return ip, ok
}

func (p *Proxy) unbindLocked(sandboxID string) {
	ip, ok := p.ipOf[sandboxID]
	if !ok {
		return
	}
	delete(p.ipOf, sandboxID)
	// Docker reuses bridge addresses, so only drop the reverse entry when it
	// still points at this sandbox.
	if cur, ok := p.byIP[ip]; ok && cur == sandboxID {
		delete(p.byIP, ip)
	}
}

// evaluatorFor resolves the sandbox owning a source address and its policy.
func (p *Proxy) evaluatorFor(remoteAddr string) (*Evaluator, string, bool) {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	id, ok := p.byIP[ip]
	if !ok {
		return nil, "", false
	}
	ev, ok := p.policies[id]
	if !ok {
		return nil, id, false
	}
	return ev, id, true
}

// Start listens on ephemeral TCP/UDP ports for redirected traffic.
func (p *Proxy) Start(ctx context.Context) error {
	listeners := make([]net.Listener, 0, 3)
	closeAll := func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}
	for range 3 {
		l, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			closeAll()
			return err
		}
		listeners = append(listeners, l)
	}
	udpAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		closeAll()
		return err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		closeAll()
		return err
	}

	p.httpLis, p.tlsLis, p.otherLis = listeners[0], listeners[1], listeners[2]
	p.udpConn = udpConn
	p.HTTPPort = p.httpLis.Addr().(*net.TCPAddr).Port
	p.TLSPort = p.tlsLis.Addr().(*net.TCPAddr).Port
	p.OtherPort = p.otherLis.Addr().(*net.TCPAddr).Port
	p.UDPPort = udpConn.LocalAddr().(*net.UDPAddr).Port

	go p.serveTCP(ctx, p.httpLis, kindHTTP)
	go p.serveTCP(ctx, p.tlsLis, kindTLS)
	go p.serveTCP(ctx, p.otherLis, kindOther)
	go p.serveUDP(ctx)
	log.Printf("egress proxy listening http=:%d tls=:%d other=:%d dns=:%d",
		p.HTTPPort, p.TLSPort, p.OtherPort, p.UDPPort)
	return nil
}

// Close stops listeners.
func (p *Proxy) Close() error {
	var err error
	for _, l := range []net.Listener{p.httpLis, p.tlsLis, p.otherLis} {
		if l == nil {
			continue
		}
		if cerr := l.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if p.udpConn != nil {
		_ = p.udpConn.Close()
	}
	return err
}

func (p *Proxy) serveTCP(ctx context.Context, lis net.Listener, kind listenerKind) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		go p.handleTCP(conn, kind)
	}
}

func (p *Proxy) handleTCP(conn net.Conn, kind listenerKind) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(connSetupTimeout))

	ev, sandboxID, ok := p.evaluatorFor(conn.RemoteAddr().String())
	if !ok {
		log.Printf("egress deny tcp: no sandbox bound to %s", conn.RemoteAddr())
		return
	}

	orig, err := originalDST(conn)
	if err != nil {
		log.Printf("egress: original dst: %v", err)
		return
	}
	host, port, err := ParseHostPort(orig)
	if err != nil {
		return
	}
	dstIP := net.ParseIP(host)
	if p.destinationDenied(dstIP) {
		log.Printf("egress deny tcp sandbox=%s dst=%s: internal range", sandboxID, orig)
		return
	}

	// The name the connection itself asserts. Unavailable on the other-ports
	// listener, where a failed peek would stall server-first protocols.
	client := net.Conn(conn)
	var hostname string
	if kind != kindOther {
		pc := newPeekConn(conn)
		client = pc
		_ = conn.SetReadDeadline(time.Now().Add(peekTimeout))
		var name string
		if kind == kindTLS {
			name, err = peekTLSSNI(pc.r)
		} else {
			name, err = peekHTTPHost(pc.r)
		}
		_ = conn.SetReadDeadline(time.Now().Add(connSetupTimeout))
		if err == nil {
			hostname = sanitizeHostname(name)
		}
	}

	decision, match := ev.AllowConnect(hostname, dstIP, port)
	if decision != Allow {
		log.Printf("egress deny tcp sandbox=%s dst=%s host=%q", sandboxID, orig, hostname)
		return
	}

	dialCtx, cancelDial := context.WithTimeout(context.Background(), dialTimeout)
	dst, err := p.dialUpstream(dialCtx, hostname, orig, port, match)
	cancelDial()
	if err != nil {
		log.Printf("egress dial sandbox=%s dst=%s host=%q: %v", sandboxID, orig, hostname, err)
		return
	}
	defer dst.Close()
	_ = conn.SetDeadline(time.Time{})
	_ = dst.SetDeadline(time.Time{})

	live := &liveConn{conn: conn, hostname: hostname, ip: dstIP, port: port}
	p.trackConn(sandboxID, live)
	defer p.untrackConn(sandboxID, live)

	proxyCopy(client, dst)
}

// dialUpstream connects to the destination the policy actually approved.
//
// A domain match approved a name, not an address, so the name is re-resolved
// here rather than trusting the guest's original destination: otherwise an
// /etc/hosts edit or a guest-side resolver could point an allowed name at an
// internal address and ride through on the SNI alone.
func (p *Proxy) dialUpstream(ctx context.Context, hostname, orig string, port uint32, match MatchType) (net.Conn, error) {
	if match != MatchDomain || hostname == "" {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", orig)
	}
	d := &net.Dialer{ControlContext: p.rejectDeniedAddr}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(hostname, strconv.FormatUint(uint64(port), 10)))
}

// rejectDeniedAddr runs after resolution but before connect(2), so every
// address Happy Eyeballs tries is checked, not just the first.
func (p *Proxy) rejectDeniedAddr(_ context.Context, _, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("egress: unparsable dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("egress: unresolved dial address %q", address)
	}
	if p.destinationDenied(ip) {
		return fmt.Errorf("egress: %s resolves into a denied internal range", address)
	}
	return nil
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
	ev, sandboxID, ok := p.evaluatorFor(client.IP.String())
	if !ok {
		log.Printf("egress deny dns: no sandbox bound to %s", client.IP)
		return
	}
	if ev.AllowDNS(name) != Allow {
		log.Printf("egress deny dns sandbox=%s name=%s", sandboxID, name)
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
