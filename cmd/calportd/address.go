package main

import (
	"errors"
	"net"
	"net/netip"
)

// carrierGradeNAT is 100.64.0.0/10, which Tailscale uses and Go's IsPrivate
// does not cover.
var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// pickAddress guesses the address a laptop should dial. A VPS usually has one
// public IPv4 address; failing that, a private or tailnet one is still useful,
// and the hostname is the last resort. `pair --address` overrides the guess.
func pickAddress(ips []net.IP, hostname string) string {
	var fallback string
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if !addr.Is4() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
			continue
		}
		if !addr.IsPrivate() && !carrierGradeNAT.Contains(addr) {
			return addr.String()
		}
		if fallback == "" {
			fallback = addr.String()
		}
	}
	if fallback != "" {
		return fallback
	}
	return hostname
}

func interfaceIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

// defaultListen is where calportd listens when --listen is not given: the
// box's tailnet address only. Answering on every interface would expose the
// daemon to the internet, which has to be asked for explicitly.
func defaultListen(ips []net.IP) (string, error) {
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if ok && addr.Unmap().Is4() && carrierGradeNAT.Contains(addr.Unmap()) {
			return net.JoinHostPort(addr.Unmap().String(), defaultPort), nil
		}
	}
	return "", errors.New("this box has no tailnet address, so calportd will not pick where to listen; " +
		"pass --listen, e.g. --listen 0.0.0.0:" + defaultPort + " to accept connections from the internet")
}

// advertise is the address a pairing link tells laptops to dial. When
// calportd listens on one specific address, that is the only one that works,
// so it wins over any guess.
func advertise(listening string, ips []net.IP, hostname string) string {
	host, port, err := net.SplitHostPort(listening)
	if err != nil {
		host, port = "", defaultPort
	}
	if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
		return listening
	}
	return net.JoinHostPort(pickAddress(ips, hostname), port)
}
