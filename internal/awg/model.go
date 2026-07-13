package awg

import "net/netip"

// Hub is the VPS relay with the public IP. There is exactly one per deployment.
type Hub struct {
	Endpoint   string     // public "IP:port" clients & nodes dial
	ListenPort int        // awg ListenPort
	Address    netip.Addr // hub tunnel IP, e.g. 10.8.0.1
	PoolCIDR   netip.Prefix
	Keys       Keypair
	Params     ObfuscationParams
	DNS        string // optional DNS pushed to clients (e.g. 10.8.0.1)
}

// Node is a home server behind CGNAT. It dials OUT to the hub and owns one or
// more LAN subnets. Its config is static once installed.
type Node struct {
	Name      string
	Address   netip.Addr     // node tunnel IP, e.g. 10.8.0.2
	Subnets   []netip.Prefix // LAN(s) it exposes, e.g. 192.168.1.0/24
	Keys      Keypair
	LANIface  string // interface facing the LAN, for masquerade (e.g. "eth0")
	Preshared string // optional PSK shared with hub
}

// Client is a VPN user (laptop/phone). Gets a /32 tunnel IP.
type Client struct {
	Name      string
	Address   netip.Addr // /32 tunnel IP, e.g. 10.8.0.10
	Keys      Keypair
	Preshared string
	DNS       string // per-client override; empty = fall back to Hub.DNS
}

// Grant links a client to a node it may reach.
type Grant struct {
	ClientAddr netip.Addr
	NodeAddr   netip.Addr
	Subnets    []netip.Prefix // copied from node at grant time
}
