package wol

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

// ParseMAC accepts common MAC formats (aa:bb:cc:dd:ee:ff, aa-bb-..., aabbccddeeff)
// and returns 6 bytes.
func ParseMAC(s string) ([]byte, error) {
	clean := strings.NewReplacer(":", "", "-", "", ".", "", " ", "").
		Replace(strings.TrimSpace(s))
	if len(clean) != 12 {
		return nil, fmt.Errorf("invalid MAC length: %d", len(clean))
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("invalid MAC: %w", err)
	}
	return b, nil
}

func formatMAC(hw []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 17)
	for i, b := range hw {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hexDigits[b>>4], hexDigits[b&0xf])
	}
	return string(out)
}

func normalizeMAC(mac string) string {
	hw, err := ParseMAC(mac)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(mac))
	}
	return formatMAC(hw)
}

// buildMagicPacket: 6 × 0xFF followed by 16 repetitions of the MAC.
func buildMagicPacket(hw []byte) []byte {
	buf := make([]byte, 6+16*len(hw))
	for i := 0; i < 6; i++ {
		buf[i] = 0xFF
	}
	for i := 1; i <= 16; i++ {
		copy(buf[6*i:], hw)
	}
	return buf
}

// Send transmits a magic packet for mac to broadcast:port.
func Send(mac, broadcast string, port int) error {
	hw, err := ParseMAC(mac)
	if err != nil {
		return err
	}
	if port <= 0 {
		port = 9
	}
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}
	conn, err := net.Dial("udp", fmt.Sprintf("%s:%d", broadcast, port))
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(buildMagicPacket(hw))
	return err
}

// SendToAll sends the magic packet to the global broadcast plus the
// directed broadcast of every local subnet that contains deviceIP.
// Duplicates are collapsed. This covers setups where the global
// broadcast is filtered but the subnet broadcast still works.
func SendToAll(mac, deviceIP, broadcast string, port int) error {
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}
	targets := map[string]struct{}{broadcast: {}}
	if deviceIP != "" {
		if ip := net.ParseIP(deviceIP); ip != nil {
			for _, n := range localSubnets() {
				if n.Contains(ip) {
					if b := broadcastFor(n); b != nil {
						targets[b.String()] = struct{}{}
					}
				}
			}
		}
	}
	var lastErr error
	for t := range targets {
		if err := Send(mac, t, port); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
