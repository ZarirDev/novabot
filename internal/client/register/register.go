package register

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/ZarirDev/novabot/internal/client/grub"
)

type Registration struct {
	Hostname string       `json:"hostname"`
	MAC      string       `json:"mac"`
	IP       string       `json:"ip"`
	BootAddr string       `json:"boot_addr"`
	Token    string       `json:"boot_token"`
	Entries  []grub.Entry `json:"entries"`
	Default  string       `json:"default,omitempty"`
}

// Register POSTs this machine's identity and GRUB entries to the server.
// The server upserts them against the WoL device whose MAC matches.
func Register(serverAddr string, httpPort int, reg Registration) error {
	url := fmt.Sprintf("http://%s:%d/api/v1/wol/boot-register", serverAddr, httpPort)

	body, err := json.Marshal(reg)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, buf.String())
	}
	return nil
}

// LocalIP returns the LAN IP used for outbound traffic.
func LocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// LocalMAC returns the MAC of the interface whose IP matches our
// outbound source. Empty on failure.
func LocalMAC() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	target := conn.LocalAddr().(*net.UDPAddr).IP

	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ipnet.IP.Equal(target) {
				return ifc.HardwareAddr.String()
			}
		}
	}
	return ""
}

func Hostname() string {
	h, err := net.LookupCNAME("localhost")
	if err != nil {
		return ""
	}
	return h
}
