package egress

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

const (
	catchAllPort  = 15000
	lastDNSWindow = 2 * time.Minute
)

type session struct {
	sandboxID  string
	networkID  string
	subnet     string
	gatewayIP  string
	sandboxIP  string // conventional .3
	ev         *Evaluator
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

// NewServer creates an unstarted gateway server.
func NewServer() *Server {
	return &Server{
		sessions: make(map[string]*session),
		byGWIP:   make(map[string]string),
		bySBIP:   make(map[string]string),
		conns:    make(map[string]map[*liveConn]bool),
	}
}

// ControlConfig configures the gRPC control listener.
type ControlConfig struct {
	// Addr is the TCP listen address (e.g. "0.0.0.0:17948").
	Addr string
	// Token is the bearer token required on every control RPC.
	Token string
}

// Start binds data-plane listeners and the token-authenticated control TCP port.
func (s *Server) Start(ctx context.Context, ctrl ControlConfig) error {
	if strings.TrimSpace(ctrl.Addr) == "" {
		return fmt.Errorf("control addr required")
	}
	if strings.TrimSpace(ctrl.Token) == "" {
		return fmt.Errorf("control token required")
	}
	s.controlToken = ctrl.Token

	httpLis, err := net.Listen("tcp", "0.0.0.0:80")
	if err != nil {
		return fmt.Errorf("listen :80: %w", err)
	}
	tlsLis, err := net.Listen("tcp", "0.0.0.0:443")
	if err != nil {
		_ = httpLis.Close()
		return fmt.Errorf("listen :443: %w", err)
	}
	otherLis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", catchAllPort))
	if err != nil {
		_ = httpLis.Close()
		_ = tlsLis.Close()
		return fmt.Errorf("listen :%d: %w", catchAllPort, err)
	}
	s.httpLis, s.tlsLis, s.otherLis = httpLis, tlsLis, otherLis

	ctrlLis, err := net.Listen("tcp", ctrl.Addr)
	if err != nil {
		s.closeListeners()
		return fmt.Errorf("listen control %s: %w", ctrl.Addr, err)
	}
	s.controlLis = ctrlLis

	s.grpcSrv = grpc.NewServer(
		grpc.UnaryInterceptor(s.authUnary),
		grpc.StreamInterceptor(s.authStream),
	)
	cellarv1.RegisterEgressGatewayControlServer(s.grpcSrv, s)

	go s.serveTCP(ctx, s.httpLis, kindHTTP)
	go s.serveTCP(ctx, s.tlsLis, kindTLS)
	go s.serveTCP(ctx, s.otherLis, kindOther)
	go func() {
		if err := s.grpcSrv.Serve(ctrlLis); err != nil {
			log.Printf("egress-gateway control: %v", err)
		}
	}()
	log.Printf("egress-gateway listening http=:80 tls=:443 other=:%d control=%s", catchAllPort, ctrl.Addr)
	return nil
}

func (s *Server) authUnary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

func (s *Server) authStream(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := s.checkAuth(ss.Context()); err != nil {
		return err
	}
	return handler(srv, ss)
}

func (s *Server) checkAuth(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return status.Error(codes.Unauthenticated, "missing authorization")
	}
	const prefix = "Bearer "
	raw := vals[0]
	if !strings.HasPrefix(raw, prefix) {
		return status.Error(codes.Unauthenticated, "invalid authorization")
	}
	token := strings.TrimSpace(strings.TrimPrefix(raw, prefix))
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.controlToken)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
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
	exceptions, err := ParseCIDRs(req.GetPrivateExceptions())
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
	sbIP := SandboxIP(subnet)

	udpConn, tcpLis, err := listenDNS(req.GetGatewayIp())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
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
		ev:         NewEvaluator(policy),
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
	ev := NewEvaluator(policy)

	s.mu.Lock()
	sess, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return nil, status.Errorf(codes.NotFound, "sandbox %s not registered", id)
	}
	sess.ev = ev
	var revoked []*liveConn
	for c := range s.conns[id] {
		if decision, _ := ev.AllowConnect(c.hostname, c.ip, c.port); decision != Allow {
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
		h, p, err := ParseHostPort(orig)
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
		pc := NewPeekConn(conn)
		client = pc
		_ = conn.SetReadDeadline(time.Now().Add(peekTimeout))
		var name string
		var err error
		if kind == kindTLS {
			name, err = PeekTLSSNI(pc.R)
		} else {
			name, err = PeekHTTPHost(pc.R)
		}
		_ = conn.SetReadDeadline(time.Now().Add(connSetupTimeout))
		if err != nil || name == "" {
			log.Printf("egress-gateway deny tcp sandbox=%s: no SNI/Host (%v)", sandboxID, err)
			return
		}
		hostname = SanitizeHostname(name)
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
		if decision != Allow || match != MatchDomain {
			log.Printf("egress-gateway deny tcp sandbox=%s port=%d host=%q (need allow_domain_ports)", sandboxID, port, hostname)
			return
		}
		dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		dst, err := s.dialUpstream(dialCtx, sess, hostname, "", port, MatchDomain)
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
		if decision != Allow {
			log.Printf("egress-gateway deny tcp sandbox=%s dst=%s port=%d", sandboxID, dstIP, port)
			return
		}
		if match == MatchNone {
			// denylist / allowall: nothing asserted; keep original IP.
			match = MatchCIDR
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
	if decision != Allow {
		log.Printf("egress-gateway deny tcp sandbox=%s host=%q port=%d", sandboxID, hostname, port)
		return
	}
	// Topology model: always dial by hostname for 80/443 domain matches.
	if match != MatchDomain {
		match = MatchDomain
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

func (s *Server) dialUpstream(ctx context.Context, sess *session, hostname, ip string, port uint32, match MatchType) (net.Conn, error) {
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
func upstreamDialAddr(hostname, ip string, port uint32, match MatchType) (string, error) {
	switch match {
	case MatchDomain:
		if hostname == "" {
			return "", fmt.Errorf("refusing empty hostname for domain dial")
		}
		return net.JoinHostPort(hostname, strconv.FormatUint(uint64(port), 10)), nil
	case MatchCIDR:
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
	if ip == nil || !IsDenied(ip) {
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
	name, err := ParseDNSQName(pkt)
	if err != nil || name == "" {
		return
	}
	s.mu.RLock()
	ev := sess.ev
	gwIP := sess.gatewayIP
	s.mu.RUnlock()

	if ev.AllowDNS(name) != Allow {
		log.Printf("egress-gateway deny dns sandbox=%s name=%s", sess.sandboxID, name)
		out, err := BuildDNSNXDomain(pkt)
		if err == nil {
			reply(out)
		}
		return
	}

	ip := net.ParseIP(gwIP).To4()
	if ip == nil {
		return
	}
	out, err := BuildDNSAResponse(pkt, ip)
	if err != nil {
		return
	}
	s.mu.Lock()
	sess.lastDNS = SanitizeHostname(name)
	sess.lastDNSAt = time.Now()
	s.mu.Unlock()
	log.Printf("egress-gateway allow dns sandbox=%s name=%s -> %s", sess.sandboxID, name, gwIP)
	reply(out)
}

// dnsBindAttempts / dnsBindRetry are vars so tests can shrink the window.
var (
	dnsBindAttempts = 20
	dnsBindRetry    = 50 * time.Millisecond
)

// listenUDP / listenTCP are net defaults, overridden in tests.
var (
	listenUDP = net.ListenUDP
	listenTCP = net.Listen
)

// listenDNS binds UDP/TCP :53 on host. Retries because Docker may not have
// assigned the gateway .2 address yet right after NetworkConnect.
func listenDNS(host string) (*net.UDPConn, net.Listener, error) {
	addr := net.JoinHostPort(host, "53")
	var last error
	for attempt := 0; attempt < dnsBindAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(dnsBindRetry)
		}
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return nil, nil, fmt.Errorf("dns udp addr: %w", err)
		}
		udpConn, err := listenUDP("udp", udpAddr)
		if err != nil {
			last = fmt.Errorf("listen dns udp %s: %w", host, err)
			continue
		}
		tcpLis, err := listenTCP("tcp", addr)
		if err != nil {
			_ = udpConn.Close()
			last = fmt.Errorf("listen dns tcp %s: %w", host, err)
			continue
		}
		return udpConn, tcpLis, nil
	}
	if last == nil {
		return nil, nil, fmt.Errorf("listen dns %s: exhausted retries", host)
	}
	return nil, nil, last
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
	// Dedicated listeners own :53 / :80 / :443 on the gateway leg. Skip
	// those ports with RETURN instead of `multiport ! --dports` — nftables
	// rejects inverted multiport ("multiport.0 does not support invert").
	except := func(dport string) []string {
		return []string{"PREROUTING", "-d", gatewayIP, "-p", "tcp", "--dport", dport, "-j", "RETURN"}
	}
	toLeg := []string{
		"PREROUTING", "-d", gatewayIP, "-p", "tcp",
		"-j", "REDIRECT", "--to-ports", port,
	}
	// Routed raw-IP path: sandbox default route via .2 preserves the original
	// destination; REDIRECT makes those packets locally delivered so
	// SO_ORIGINAL_DST recovers the real ip:port for CIDR policy.
	routed := []string{
		"PREROUTING", "-s", sandboxIP, "!", "-d", gatewayIP, "-p", "tcp",
		"-j", "REDIRECT", "--to-ports", port,
	}
	return [][]string{except("53"), except("80"), except("443"), toLeg, routed}
}

func runIPTables(action string, spec []string) error {
	args := append([]string{"-t", "nat", action, spec[0]}, spec[1:]...)
	if out, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("iptables %v: %w (%s)", args, err, string(out))
	}
	return nil
}
