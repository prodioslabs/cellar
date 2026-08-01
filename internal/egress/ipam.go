package egress

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

const (
	// DefaultSupernet is the default address pool for sandbox networks.
	DefaultSupernet = "172.30.0.0/16"
	// PrefixLen is the size of each allocated subnet (/29).
	PrefixLen     = 29
	gatewayOffset = 2
	sandboxOffset = 3
)

// Allocator hands out /29s from a supernet and persists assignments to disk.
type Allocator struct {
	mu       sync.Mutex
	supernet *net.IPNet
	// assigned maps sandbox ID -> subnet CIDR string
	assigned map[string]string
	// free is a LIFO of unused subnet CIDR strings
	free []string
	path string
}

type persistState struct {
	Supernet string            `json:"supernet"`
	Assigned map[string]string `json:"assigned"`
	Free     []string          `json:"free"`
}

// NewAllocator loads state from path if present, otherwise initializes from supernetCIDR.
func NewAllocator(dataDir, supernetCIDR string) (*Allocator, error) {
	if supernetCIDR == "" {
		supernetCIDR = DefaultSupernet
	}
	_, supernet, err := net.ParseCIDR(supernetCIDR)
	if err != nil {
		return nil, fmt.Errorf("egress supernet: %w", err)
	}
	ones, bits := supernet.Mask.Size()
	if bits != 32 || ones > PrefixLen {
		return nil, fmt.Errorf("egress supernet %s must be an IPv4 network larger than /%d", supernetCIDR, PrefixLen)
	}

	dir := filepath.Join(dataDir, "egress")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "ipam.json")

	a := &Allocator{
		supernet: supernet,
		assigned: make(map[string]string),
		path:     path,
	}
	if err := a.loadOrInit(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Allocator) loadOrInit() error {
	data, err := os.ReadFile(a.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		a.free = generateAll(a.supernet)
		return a.saveLocked()
	}
	var st persistState
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("ipam state: %w", err)
	}
	if st.Supernet != a.supernet.String() {
		// Supernet changed: rebuild free list, keep only assignments still inside.
		a.assigned = make(map[string]string)
		a.free = generateAll(a.supernet)
		return a.saveLocked()
	}
	if st.Assigned == nil {
		st.Assigned = make(map[string]string)
	}
	a.assigned = st.Assigned
	a.free = st.Free
	if len(a.free) == 0 && len(a.assigned) == 0 {
		a.free = generateAll(a.supernet)
		return a.saveLocked()
	}
	return nil
}

func (a *Allocator) saveLocked() error {
	st := persistState{
		Supernet: a.supernet.String(),
		Assigned: a.assigned,
		Free:     a.free,
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

// Allocate reserves a /29 for sandboxID. Idempotent if already allocated.
func (a *Allocator) Allocate(sandboxID string) (*net.IPNet, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cidr, ok := a.assigned[sandboxID]; ok {
		_, n, err := net.ParseCIDR(cidr)
		return n, err
	}
	if len(a.free) == 0 {
		return nil, fmt.Errorf("egress ipam: no free /%d subnets in %s", PrefixLen, a.supernet)
	}
	cidr := a.free[len(a.free)-1]
	a.free = a.free[:len(a.free)-1]
	a.assigned[sandboxID] = cidr
	if err := a.saveLocked(); err != nil {
		a.free = append(a.free, cidr)
		delete(a.assigned, sandboxID)
		return nil, err
	}
	_, n, err := net.ParseCIDR(cidr)
	return n, err
}

// Free returns the sandbox's /29 to the free list. Idempotent.
func (a *Allocator) Free(sandboxID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cidr, ok := a.assigned[sandboxID]
	if !ok {
		return nil
	}
	delete(a.assigned, sandboxID)
	a.free = append(a.free, cidr)
	return a.saveLocked()
}

// Adopt forces sandboxID to own cidr (removing it from free if present).
// Used when reusing an existing Docker network whose subnet must win.
func (a *Allocator) Adopt(sandboxID, cidr string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, _, err := net.ParseCIDR(cidr); err != nil {
		return fmt.Errorf("adopt: invalid cidr %q: %w", cidr, err)
	}
	if prev, ok := a.assigned[sandboxID]; ok && prev != cidr {
		a.free = append(a.free, prev)
	}
	// Drop cidr from free if present.
	next := a.free[:0]
	for _, f := range a.free {
		if f != cidr {
			next = append(next, f)
		}
	}
	a.free = next
	// If another sandbox owns this cidr, steal it (orphan recovery).
	for id, c := range a.assigned {
		if c == cidr && id != sandboxID {
			delete(a.assigned, id)
		}
	}
	a.assigned[sandboxID] = cidr
	return a.saveLocked()
}

// Lookup returns the allocated subnet for a sandbox, if any.
func (a *Allocator) Lookup(sandboxID string) (*net.IPNet, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cidr, ok := a.assigned[sandboxID]
	if !ok {
		return nil, false
	}
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, false
	}
	return n, true
}

// SyncFromDocker rebuilds assigned/free from live Docker-labeled subnet map
// (sandboxID -> CIDR). Orphans in assigned that are not in live are freed;
// live entries missing from assigned are adopted.
func (a *Allocator) SyncFromDocker(live map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	used := make(map[string]bool, len(live))
	a.assigned = make(map[string]string, len(live))
	for id, cidr := range live {
		a.assigned[id] = cidr
		used[cidr] = true
	}
	all := generateAll(a.supernet)
	a.free = a.free[:0]
	for _, cidr := range all {
		if !used[cidr] {
			a.free = append(a.free, cidr)
		}
	}
	return a.saveLocked()
}

// AssignedCopy returns a snapshot of sandboxID -> subnet CIDR.
func (a *Allocator) AssignedCopy() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]string, len(a.assigned))
	for k, v := range a.assigned {
		out[k] = v
	}
	return out
}

// GatewayIP returns the conventional .2 address within subnet.
func GatewayIP(subnet *net.IPNet) net.IP {
	return offsetIP(subnet, gatewayOffset)
}

// SandboxIP returns the conventional .3 address within subnet.
func SandboxIP(subnet *net.IPNet) net.IP {
	return offsetIP(subnet, sandboxOffset)
}

func offsetIP(subnet *net.IPNet, offset int) net.IP {
	ip := subnet.IP.To4()
	if ip == nil {
		return nil
	}
	out := make(net.IP, 4)
	copy(out, ip)
	// Add offset to the last octet (safe for /29 aligned on 8-address boundaries).
	out[3] += byte(offset)
	return out
}

func generateAll(supernet *net.IPNet) []string {
	ones, bits := supernet.Mask.Size()
	hostBits := bits - ones
	blockBits := bits - PrefixLen
	if blockBits > hostBits {
		return nil
	}
	nBlocks := 1 << (hostBits - blockBits)
	base := ipToUint32(supernet.IP.To4())
	stride := uint32(1 << blockBits)
	out := make([]string, 0, nBlocks)
	for i := 0; i < nBlocks; i++ {
		ip := uint32ToIP(base + uint32(i)*stride)
		out = append(out, fmt.Sprintf("%s/%d", ip, PrefixLen))
	}
	return out
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}
