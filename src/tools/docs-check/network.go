package main

import (
	"errors"
	"net/netip"
	"strings"
)

// ipNet mirrors the subset of Python ipaddress.ip_network used by the policy
// example check: a masked CIDR with a version and an overlap test.
type ipNet struct {
	p netip.Prefix
}

func (n *ipNet) prefixLen() int { return n.p.Bits() }

func (n *ipNet) sameVersion(o *ipNet) bool { return n.p.Addr().Is4() == o.p.Addr().Is4() }

func (n *ipNet) overlaps(o *ipNet) bool { return n.p.Overlaps(o.p) }

// parseNetwork parses raw like Python ipaddress.ip_network(raw, strict=True):
// an explicit prefix must not have host bits set, a bare address becomes a
// /32 (IPv4) or /128 (IPv6) host network.
func parseNetwork(raw string) (*ipNet, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "/") {
		p, e := netip.ParsePrefix(raw)
		if e != nil {
			return nil, errors.New("invalid source network")
		}
		if p.Addr() != p.Masked().Addr() {
			return nil, errors.New("source network has host bits set")
		}
		return &ipNet{p: p.Masked()}, nil
	}
	addr, e := netip.ParseAddr(raw)
	if e != nil {
		return nil, errors.New("invalid source network")
	}
	return &ipNet{p: netip.PrefixFrom(addr, addr.BitLen())}, nil
}
