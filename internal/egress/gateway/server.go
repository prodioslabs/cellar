// Package gateway implements the topology-based egress gateway data plane
// and gRPC control server (TCP + bearer token).
package gateway

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

const (
	connSetupTimeout = 60 * time.Second
	peekTimeout      = 5 * time.Second
	dialTimeout      = 15 * time.Second
	dnsTTL           = 10
	catchAllPort     = 15000
	lastDNSWindow    = 2 * time.Minute
)

type listenerKind int

const (
	kindOther listenerKind = iota
	kindHTTP
	kindTLS
)

type liveConn struct {
	conn     net.Conn
	hostname string
	ip       net.IP
	port     uint32
}

type session struct {
	sandboxID  string
	networkID  string
	subnet     string
	gatewayIP  string
	sandboxIP  string // conventional .3
	ev         *egress.Evaluator
	exceptions []*net.IPNet
	lastDNS    string // last allowed DNS name
	lastDNSAt  time.Time
	dnsUDP     *net.UDPConn
	dnsTCP     net.Listener
}

// Server is the egress gateway process.
type Server struct {
	cellarv1.UnimplementedEgressGatewayControlServer

	mu       sync.RWMutex
	sessions map[string]*session // sandboxID -> session
	byGWIP   map[string]string   // gateway .2 -> sandboxID
	bySBIP   map[string]string   // sandbox .3 -> sandboxID
	conns    map[string]map[*liveConn]bool

	httpLis  net.Listener
	tlsLis   net.Listener
	otherLis net.Listener

	controlLis   net.Listener
	controlToken string
	grpcSrv      *grpc.Server
}

// New creates an unstarted gateway server.
func New() *Server {
	return &Server{
		sessions: make(map[string]*session),
		byGWIP:   make(map[string]string),
		bySBIP:   make(map[string]string),
		conns:    make(map[string]map[*liveConn]bool),
	}
}

// Close stops listeners and the control server.
func (s *Server) Close() error {
	if s.grpcSrv != nil {
		s.grpcSrv.GracefulStop()
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_, _ = s.DeregisterSandbox(context.Background(), &cellarv1.DeregisterSandboxRequest{SandboxId: id})
	}
	s.closeListeners()
	return nil
}

func (s *Server) closeListeners() {
	for _, l := range []net.Listener{s.httpLis, s.tlsLis, s.otherLis, s.controlLis} {
		if l != nil {
			_ = l.Close()
		}
	}
}

func (s *Server) RegisterSandbox(_ context.Context, req *cellarv1.RegisterSandboxRequest) (*cellarv1.RegisterSandboxResponse, error) {
	if req.GetSandboxId() == "" || req.GetGatewayIp() == "" || req.GetSubnetCidr() == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id, gateway_ip, subnet_cidr required")
	}
	policy := sandbox.NetworkPolicyFromProto(req.GetPolicy())
	exceptions, err := egress.ParseCIDRs(req.GetPrivateExceptions())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "private_exceptions: %v", err)
	}
	gwIP := net.ParseIP(req.GetGatewayIp())
	if gwIP == nil || gwIP.To4() == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid gateway_ip")
	}
	_, subnet, err := net.ParseCIDR(req.GetSubnetCidr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "subnet: %v", err)
	}
	sbIP := offsetInSubnet(subnet, 3)

	udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(req.GetGatewayIp(), "53"))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "dns udp addr: %v", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "listen dns udp %s: %v", req.GetGatewayIp(), err)
	}
	tcpLis, err := net.Listen("tcp", net.JoinHostPort(req.GetGatewayIp(), "53"))
	if err != nil {
		_ = udpConn.Close()
		return nil, status.Errorf(codes.Internal, "listen dns tcp %s: %v", req.GetGatewayIp(), err)
	}

	if err := addCatchAllRedirect(req.GetGatewayIp(), sbIP.String()); err != nil {
		_ = udpConn.Close()
		_ = tcpLis.Close()
		return nil, status.Errorf(codes.Internal, "iptables: %v", err)
	}

	sess := &session{
		sandboxID:  req.GetSandboxId(),
		networkID:  req.GetNetworkId(),
		subnet:     req.GetSubnetCidr(),
		gatewayIP:  req.GetGatewayIp(),
		sandboxIP:  sbIP.String(),
		ev:         egress.NewEvaluator(policy),
		exceptions: exceptions,
		dnsUDP:     udpConn,
		dnsTCP:     tcpLis,
	}

	s.mu.Lock()
	if prev, ok := s.sessions[req.GetSandboxId()]; ok {
		s.dropSessionLocked(prev)
	}
	s.sessions[req.GetSandboxId()] = sess
	s.byGWIP[req.GetGatewayIp()] = req.GetSandboxId()
	s.bySBIP[sbIP.String()] = req.GetSandboxId()
	s.mu.Unlock()

	go s.serveDNSUDP(sess)
	go s.serveDNSTCP(sess)
	log.Printf("egress-gateway register sandbox=%s gw=%s subnet=%s", req.GetSandboxId(), req.GetGatewayIp(), req.GetSubnetCidr())
	return &cellarv1.RegisterSandboxResponse{}, nil
}

func (s *Server) DeregisterSandbox(_ context.Context, req *cellarv1.DeregisterSandboxRequest) (*cellarv1.DeregisterSandboxResponse, error) {
	id := req.GetSandboxId()
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if ok {
		s.dropSessionLocked(sess)
	}
	open := s.conns[id]
	delete(s.conns, id)
	s.mu.Unlock()
	for c := range open {
		_ = c.conn.Close()
	}
	if ok {
		log.Printf("egress-gateway deregister sandbox=%s", id)
	}
	return &cellarv1.DeregisterSandboxResponse{}, nil
}

func (s *Server) UpdatePolicy(_ context.Context, req *cellarv1.UpdatePolicyRequest) (*cellarv1.UpdatePolicyResponse, error) {
	id := req.GetSandboxId()
	policy := sandbox.NetworkPolicyFromProto(req.GetPolicy())
	ev := egress.NewEvaluator(policy)

	s.mu.Lock()
	sess, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return nil, status.Errorf(codes.NotFound, "sandbox %s not registered", id)
	}
	sess.ev = ev
	var revoked []*liveConn
	for c := range s.conns[id] {
		if decision, _ := ev.AllowConnect(c.hostname, c.ip, c.port); decision != egress.Allow {
			revoked = append(revoked, c)
		}
	}
	s.mu.Unlock()

	for _, c := range revoked {
		log.Printf("egress-gateway revoke tcp sandbox=%s dst=%s host=%q", id, c.ip, c.hostname)
		_ = c.conn.Close()
	}
	return &cellarv1.UpdatePolicyResponse{}, nil
}

func (s *Server) dropSessionLocked(sess *session) {
	delete(s.sessions, sess.sandboxID)
	delete(s.byGWIP, sess.gatewayIP)
	delete(s.bySBIP, sess.sandboxIP)
	if sess.dnsUDP != nil {
		_ = sess.dnsUDP.Close()
	}
	if sess.dnsTCP != nil {
		_ = sess.dnsTCP.Close()
	}
	_ = deleteCatchAllRedirect(sess.gatewayIP, sess.sandboxIP)
}

func (s *Server) sessionByLocalIP(local string) (*session, string, bool) {
	ip, _, err := net.SplitHostPort(local)
	if err != nil {
		ip = local
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byGWIP[ip]
	if !ok {
		return nil, "", false
	}
	sess, ok := s.sessions[id]
	return sess, id, ok
}

func (s *Server) sessionByRemoteIP(remote string) (*session, string, bool) {
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.bySBIP[ip]
	if !ok {
		id, ok = s.byGWIP[ip]
	}
	if !ok {
		return nil, "", false
	}
	sess, ok := s.sessions[id]
	return sess, id, ok
}

func (s *Server) serveTCP(ctx context.Context, lis net.Listener, kind listenerKind) {
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
		go s.handleTCP(conn, kind)
	}
}

func (s *Server) handleTCP(conn net.Conn, kind listenerKind) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(connSetupTimeout))

	sess, sandboxID, ok := s.sessionByLocalIP(conn.LocalAddr().String())
	if !ok {
		// After REDIRECT, LocalAddr is the catch-all; attribute by remote (.3).
		sess, sandboxID, ok = s.sessionByRemoteIP(conn.RemoteAddr().String())
	}
	if !ok {
		log.Printf("egress-gateway deny tcp: no sandbox for local=%s remote=%s", conn.LocalAddr(), conn.RemoteAddr())
		return
	}

	var port uint32
	var dstIP net.IP

	if kind == kindOther {
		orig, err := originalDST(conn)
		if err != nil {
			log.Printf("egress-gateway: original dst: %v", err)
			return
		}
		h, p, err := egress.ParseHostPort(orig)
		if err != nil {
			return
		}
		port = p
		dstIP = net.ParseIP(h)
	} else {
		// Guest connected to gateway .2 believing it is the destination.
		port = 80
		if kind == kindTLS {
			port = 443
		}
		dstIP = net.ParseIP(sess.gatewayIP)
	}

	client := net.Conn(conn)
	var hostname string
	if kind != kindOther {
		pc := egress.NewPeekConn(conn)
		client = pc
		_ = conn.SetReadDeadline(time.Now().Add(peekTimeout))
		var name string
		var err error
		if kind == kindTLS {
			name, err = egress.PeekTLSSNI(pc.R)
		} else {
			name, err = egress.PeekHTTPHost(pc.R)
		}
		_ = conn.SetReadDeadline(time.Now().Add(connSetupTimeout))
		if err != nil || name == "" {
			log.Printf("egress-gateway deny tcp sandbox=%s: no SNI/Host (%v)", sandboxID, err)
			return
		}
		hostname = egress.SanitizeHostname(name)
		if hostname == "" {
			log.Printf("egress-gateway deny tcp sandbox=%s: empty hostname", sandboxID)
			return
		}
	} else if dstIP != nil && dstIP.Equal(net.ParseIP(sess.gatewayIP)) {
		// DNS-bait path for non-standard ports: original dst is the gateway
		// leg itself. Require an explicit domain:port allow via last DNS.
		s.mu.RLock()
		last := sess.lastDNS
		fresh := time.Since(sess.lastDNSAt) < lastDNSWindow
		ev := sess.ev
		s.mu.RUnlock()
		if fresh && last != "" {
			hostname = last
		}
		decision, match := ev.AllowConnect(hostname, nil, port)
		if decision != egress.Allow || match != egress.MatchDomain {
			log.Printf("egress-gateway deny tcp sandbox=%s port=%d host=%q (need allow_domain_ports)", sandboxID, port, hostname)
			return
		}
		dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		dst, err := s.dialUpstream(dialCtx, sess, hostname, "", port, egress.MatchDomain)
		cancel()
		if err != nil {
			log.Printf("egress-gateway dial sandbox=%s host=%q port=%d: %v", sandboxID, hostname, port, err)
			return
		}
		defer dst.Close()
		_ = conn.SetDeadline(time.Time{})
		live := &liveConn{conn: conn, hostname: hostname, ip: nil, port: port}
		s.trackConn(sandboxID, live)
		defer s.untrackConn(sandboxID, live)
		log.Printf("egress-gateway allow tcp sandbox=%s host=%q port=%d verdict=allow", sandboxID, hostname, port)
		proxyCopy(client, dst)
		return
	} else {
		// Routed raw-IP path: sandbox default route via .2 preserved the
		// original destination. Evaluate CIDR/IP policy and dial by IP.
		if dstIP == nil {
			log.Printf("egress-gateway deny tcp sandbox=%s: no original destination", sandboxID)
			return
		}
		if s.destinationDenied(sess, dstIP) {
			log.Printf("egress-gateway deny tcp sandbox=%s dst=%s port=%d (denied range)", sandboxID, dstIP, port)
			return
		}
		s.mu.RLock()
		ev := sess.ev
		s.mu.RUnlock()
		decision, match := ev.AllowConnect("", dstIP, port)
		if decision != egress.Allow {
			log.Printf("egress-gateway deny tcp sandbox=%s dst=%s port=%d", sandboxID, dstIP, port)
			return
		}
		if match == egress.MatchNone {
			// denylist / allowall: nothing asserted; keep original IP.
			match = egress.MatchCIDR
		}
		dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		dst, err := s.dialUpstream(dialCtx, sess, "", dstIP.String(), port, match)
		cancel()
		if err != nil {
			log.Printf("egress-gateway dial sandbox=%s dst=%s port=%d: %v", sandboxID, dstIP, port, err)
			return
		}
		defer dst.Close()
		_ = conn.SetDeadline(time.Time{})
		_ = dst.SetDeadline(time.Time{})
		live := &liveConn{conn: conn, hostname: "", ip: dstIP, port: port}
		s.trackConn(sandboxID, live)
		defer s.untrackConn(sandboxID, live)
		log.Printf("egress-gateway allow tcp sandbox=%s dst=%s port=%d verdict=allow", sandboxID, dstIP, port)
		proxyCopy(client, dst)
		return
	}

	decision, match := sess.ev.AllowConnect(hostname, nil, port)
	if decision != egress.Allow {
		log.Printf("egress-gateway deny tcp sandbox=%s host=%q port=%d", sandboxID, hostname, port)
		return
	}
	// Topology model: always dial by hostname for 80/443 domain matches.
	if match != egress.MatchDomain {
		match = egress.MatchDomain
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	dst, err := s.dialUpstream(dialCtx, sess, hostname, "", port, match)
	cancel()
	if err != nil {
		log.Printf("egress-gateway dial sandbox=%s host=%q port=%d: %v", sandboxID, hostname, port, err)
		return
	}
	defer dst.Close()
	_ = conn.SetDeadline(time.Time{})
	_ = dst.SetDeadline(time.Time{})

	live := &liveConn{conn: conn, hostname: hostname, port: port}
	s.trackConn(sandboxID, live)
	defer s.untrackConn(sandboxID, live)
	log.Printf("egress-gateway allow tcp sandbox=%s host=%q port=%d verdict=allow", sandboxID, hostname, port)
	proxyCopy(client, dst)
}

func (s *Server) dialUpstream(ctx context.Context, sess *session, hostname, ip string, port uint32, match egress.MatchType) (net.Conn, error) {
	target, err := upstreamDialAddr(hostname, ip, port, match)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{ControlContext: func(ctx context.Context, network, address string, c syscall.RawConn) error {
		return s.rejectDeniedAddr(sess, address)
	}}
	return d.DialContext(ctx, "tcp", target)
}

// upstreamDialAddr picks the dial target for a policy match.
func upstreamDialAddr(hostname, ip string, port uint32, match egress.MatchType) (string, error) {
	switch match {
	case egress.MatchDomain:
		if hostname == "" {
			return "", fmt.Errorf("refusing empty hostname for domain dial")
		}
		return net.JoinHostPort(hostname, strconv.FormatUint(uint64(port), 10)), nil
	case egress.MatchCIDR:
		if ip == "" || net.ParseIP(ip) == nil {
			return "", fmt.Errorf("refusing invalid ip for cidr dial: %q", ip)
		}
		return net.JoinHostPort(ip, strconv.FormatUint(uint64(port), 10)), nil
	default:
		return "", fmt.Errorf("refusing dial with match %q", match)
	}
}

func (s *Server) rejectDeniedAddr(sess *session, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("egress: unparsable dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("egress: unresolved dial address %q", address)
	}
	if s.destinationDenied(sess, ip) {
		return fmt.Errorf("egress: %s resolves into a denied internal range", address)
	}
	return nil
}

func (s *Server) destinationDenied(sess *session, ip net.IP) bool {
	if ip == nil || !egress.IsDenied(ip) {
		return false
	}
	return !cidrsContain(sess.exceptions, ip)
}

func (s *Server) trackConn(sandboxID string, c *liveConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns[sandboxID] == nil {
		s.conns[sandboxID] = make(map[*liveConn]bool)
	}
	s.conns[sandboxID][c] = true
}

func (s *Server) untrackConn(sandboxID string, c *liveConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.conns[sandboxID]
	if set == nil {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(s.conns, sandboxID)
	}
}

func (s *Server) serveDNSUDP(sess *session) {
	buf := make([]byte, 1500)
	for {
		n, addr, err := sess.dnsUDP.ReadFromUDP(buf)
		if err != nil {
			return
		}
		s.handleDNS(sess, append([]byte(nil), buf[:n]...), func(out []byte) {
			_, _ = sess.dnsUDP.WriteToUDP(out, addr)
		})
	}
}

func (s *Server) serveDNSTCP(sess *session) {
	for {
		conn, err := sess.dnsTCP.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(connSetupTimeout))
			var hdr [2]byte
			if _, err := io.ReadFull(c, hdr[:]); err != nil {
				return
			}
			l := int(binary.BigEndian.Uint16(hdr[:]))
			if l <= 0 || l > 65535 {
				return
			}
			pkt := make([]byte, l)
			if _, err := io.ReadFull(c, pkt); err != nil {
				return
			}
			s.handleDNS(sess, pkt, func(out []byte) {
				var lenbuf [2]byte
				binary.BigEndian.PutUint16(lenbuf[:], uint16(len(out)))
				_, _ = c.Write(lenbuf[:])
				_, _ = c.Write(out)
			})
		}(conn)
	}
}

func (s *Server) handleDNS(sess *session, pkt []byte, reply func([]byte)) {
	name, err := egress.ParseDNSQName(pkt)
	if err != nil || name == "" {
		return
	}
	s.mu.RLock()
	ev := sess.ev
	gwIP := sess.gatewayIP
	s.mu.RUnlock()

	if ev.AllowDNS(name) != egress.Allow {
		log.Printf("egress-gateway deny dns sandbox=%s name=%s", sess.sandboxID, name)
		out, err := egress.BuildDNSNXDomain(pkt)
		if err == nil {
			reply(out)
		}
		return
	}

	ip := net.ParseIP(gwIP).To4()
	if ip == nil {
		return
	}
	out, err := egress.BuildDNSAResponse(pkt, ip)
	if err != nil {
		return
	}
	s.mu.Lock()
	sess.lastDNS = egress.SanitizeHostname(name)
	sess.lastDNSAt = time.Now()
	s.mu.Unlock()
	log.Printf("egress-gateway allow dns sandbox=%s name=%s -> %s", sess.sandboxID, name, gwIP)
	reply(out)
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

func offsetInSubnet(subnet *net.IPNet, offset int) net.IP {
	ip := subnet.IP.To4()
	if ip == nil {
		return nil
	}
	out := make(net.IP, 4)
	copy(out, ip)
	out[3] += byte(offset)
	return out
}

func cidrsContain(nets []*net.IPNet, ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func addCatchAllRedirect(gatewayIP, sandboxIP string) error {
	specs := catchAllSpecs(gatewayIP, sandboxIP)
	var applied [][]string
	for _, spec := range specs {
		if err := runIPTables("-C", spec); err == nil {
			continue // already present
		}
		if err := runIPTables("-A", spec); err != nil {
			for _, done := range applied {
				_ = runIPTables("-D", done)
			}
			return err
		}
		applied = append(applied, spec)
	}
	return nil
}

func deleteCatchAllRedirect(gatewayIP, sandboxIP string) error {
	var first error
	for _, spec := range catchAllSpecs(gatewayIP, sandboxIP) {
		if err := runIPTables("-D", spec); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func catchAllSpecs(gatewayIP, sandboxIP string) [][]string {
	port := strconv.Itoa(catchAllPort)
	// DNS-bait path: traffic destined to this gateway leg on non-80/443/53
	// ports (domain allowlist with explicit ports).
	toLeg := []string{
		"PREROUTING", "-d", gatewayIP, "-p", "tcp",
		"-m", "multiport", "!", "--dports", "53,80,443",
		"-j", "REDIRECT", "--to-ports", port,
	}
	// Routed raw-IP path: sandbox default route via .2 preserves the original
	// destination; REDIRECT makes those packets locally delivered so
	// SO_ORIGINAL_DST recovers the real ip:port for CIDR policy.
	routed := []string{
		"PREROUTING", "-s", sandboxIP, "!", "-d", gatewayIP, "-p", "tcp",
		"-j", "REDIRECT", "--to-ports", port,
	}
	return [][]string{toLeg, routed}
}

func runIPTables(action string, spec []string) error {
	args := append([]string{"-t", "nat", action, spec[0]}, spec[1:]...)
	if out, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("iptables %v: %w (%s)", args, err, string(out))
	}
	return nil
}
