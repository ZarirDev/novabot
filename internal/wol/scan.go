package wol

import (
	"bufio"
	"context"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var macRe = regexp.MustCompile(`(?i)^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

// localSubnets returns IPv4 networks on up, non-loopback interfaces.
func localSubnets() []*net.IPNet {
	var nets []*net.IPNet
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			nets = append(nets, ipnet)
		}
	}
	return nets
}

func broadcastFor(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	if ip == nil || len(n.Mask) != 4 {
		return nil
	}
	b := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		b[i] = ip[i] | ^n.Mask[i]
	}
	return b
}

func pingOnce(ctx context.Context, host string) {
	c, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_ = exec.CommandContext(c, "ping", "-c", "1", "-W", "1", host).Run()
}

// pingSweep pings every host in n to populate the kernel ARP cache.
// Refuses to run on networks larger than /16 or smaller than /30.
func pingSweep(ctx context.Context, n *net.IPNet) {
	ip := n.IP.To4()
	if ip == nil {
		return
	}
	ones, bits := n.Mask.Size()
	if bits != 32 || ones < 16 || ones > 30 {
		return
	}
	network := binary.BigEndian.Uint32(ip) & binary.BigEndian.Uint32(n.Mask)
	count := uint32(1) << uint(32-ones)
	if count <= 2 {
		return
	}

	sem := make(chan struct{}, 128)
	var wg sync.WaitGroup
	for i := uint32(1); i < count-1; i++ {
		if ctx.Err() != nil {
			break
		}
		addr := network + i
		wg.Add(1)
		sem <- struct{}{}
		go func(a uint32) {
			defer wg.Done()
			defer func() { <-sem }()
			buf := make(net.IP, 4)
			binary.BigEndian.PutUint32(buf, a)
			pingOnce(ctx, buf.String())
		}(addr)
	}
	wg.Wait()
}

// ReadARP parses the kernel's neighbour table.
func ReadARP() []Device {
	if data, err := os.ReadFile("/proc/net/arp"); err == nil {
		if devs := parseProcNetARP(data); len(devs) > 0 {
			return devs
		}
	}
	out, err := exec.Command("ip", "neigh", "show").Output()
	if err != nil {
		return nil
	}
	return parseIPNeigh(string(out))
}

func parseProcNetARP(data []byte) []Device {
	var out []Device
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 6 {
			continue
		}
		ip := f[0]
		mac := strings.ToLower(f[3])
		if mac == "00:00:00:00:00:00" || !macRe.MatchString(mac) {
			continue
		}
		out = append(out, Device{Name: ip, MAC: mac, IP: ip, Port: 9})
	}
	return out
}

func parseIPNeigh(out string) []Device {
	var devs []Device
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		ip := f[0]
		var mac string
		for i, tok := range f {
			if tok == "lladdr" && i+1 < len(f) {
				mac = strings.ToLower(f[i+1])
				break
			}
		}
		if mac == "" || !macRe.MatchString(mac) {
			continue
		}
		devs = append(devs, Device{Name: ip, MAC: mac, IP: ip, Port: 9})
	}
	return devs
}

// ScanLAN pings every host on each local subnet, then reads back the
// resulting ARP table. Takes a few seconds on a /24.
func ScanLAN(ctx context.Context) []Device {
	var wg sync.WaitGroup
	for _, n := range localSubnets() {
		wg.Add(1)
		go func(n *net.IPNet) {
			defer wg.Done()
			pingSweep(ctx, n)
		}(n)
	}
	wg.Wait()
	return ReadARP()
}
