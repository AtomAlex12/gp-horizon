// Package amneziawg handles the Entware userspace WARP transport.
package amneziawg

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

const maxProfileSize = 32 << 10

type profile struct {
	IPv4         netip.Prefix
	MTU          int
	Endpoint     netip.AddrPort
	DeviceConfig string
}

// parseProfile extracts the network setup and produces an awg setconf payload.
// The wg-quick routing, DNS and hook directives are never executed.
func parseProfile(r io.Reader) (profile, error) {
	p := profile{MTU: 1280}
	s := bufio.NewScanner(r)
	var device strings.Builder
	section := ""
	seen := map[string]bool{}
	seenPeer, defaultIPv4 := false, false
	var line, size int
	for s.Scan() {
		line++
		size += len(s.Bytes()) + 1
		raw, _, _ := strings.Cut(s.Text(), "#")
		raw = strings.TrimSpace(raw)
		if size > maxProfileSize {
			return profile{}, errors.New("профиль AmneziaWG слишком большой")
		}
		if raw == "" {
			continue
		}
		switch {
		case raw == "[Interface]" && section == "":
			section = "interface"
		case raw == "[Peer]" && section == "interface":
			section, seenPeer = "peer", true
		default:
			key, value, ok := strings.Cut(raw, "=")
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			id := section + "/" + key
			if !ok || value == "" || seen[id] {
				return profile{}, badProfileLine(line)
			}
			seen[id] = true
			write, err := p.option(section, key, value, &defaultIPv4)
			if err != nil {
				return profile{}, badProfileLine(line)
			}
			if write {
				fmt.Fprintf(&device, "%s = %s\n", key, value)
			}
			continue
		}
		device.WriteString(raw + "\n")
	}
	if s.Err() != nil {
		return profile{}, errors.New("не удалось прочитать профиль AmneziaWG")
	}
	if !seenPeer || !seen["interface/PrivateKey"] || !seen["peer/PublicKey"] ||
		!p.IPv4.IsValid() || !p.Endpoint.IsValid() || !defaultIPv4 {
		return profile{}, errors.New("нужен профиль AmneziaWG с одним пиром, IPv4-адресом /32, IP endpoint и AllowedIPs 0.0.0.0/0")
	}
	p.DeviceConfig = device.String()
	return p, nil
}

func badProfileLine(line int) error {
	return fmt.Errorf("профиль AmneziaWG: неверный или неподдерживаемый параметр в строке %d", line)
}

var errProfileOption = errors.New("invalid AWG option")

func (p *profile) option(section, key, value string, defaultIPv4 *bool) (bool, error) {
	switch section + "/" + key {
	case "interface/Address":
		for _, item := range strings.Split(value, ",") {
			address, err := netip.ParsePrefix(strings.TrimSpace(item))
			if err != nil {
				return false, errProfileOption
			}
			if address.Addr().Is4() {
				if p.IPv4.IsValid() || address.Bits() != 32 || !address.Addr().IsGlobalUnicast() {
					return false, errProfileOption
				}
				p.IPv4 = address
			}
		}
		return false, nil
	case "interface/MTU":
		mtu, err := strconv.Atoi(value)
		if err != nil || mtu < 576 || mtu > 1500 {
			return false, errProfileOption
		}
		p.MTU = mtu
		return false, nil
	case "interface/DNS", "interface/Table":
		return false, nil
	case "interface/PrivateKey", "peer/PublicKey", "peer/PresharedKey":
		decoded, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return false, errProfileOption
		}
	case "peer/Endpoint":
		endpoint, err := netip.ParseAddrPort(value)
		if err != nil || !endpoint.Addr().IsGlobalUnicast() || endpoint.Addr().Zone() != "" || endpoint.Port() == 0 {
			return false, errProfileOption
		}
		p.Endpoint = endpoint
	case "peer/AllowedIPs":
		for _, item := range strings.Split(value, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
			if err != nil {
				return false, errProfileOption
			}
			if prefix == netip.PrefixFrom(netip.IPv4Unspecified(), 0) {
				*defaultIPv4 = true
			}
		}
	case "interface/Jc", "interface/Jmin", "interface/Jmax",
		"interface/S1", "interface/S2", "interface/S3", "interface/S4",
		"interface/H1", "interface/H2", "interface/H3", "interface/H4",
		"interface/I1", "interface/I2", "interface/I3", "interface/I4", "interface/I5",
		"peer/PersistentKeepalive":
		// Let the installed AWG version validate its protocol-specific settings.
	default:
		return false, errProfileOption
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return false, errProfileOption
	}
	return true, nil
}
