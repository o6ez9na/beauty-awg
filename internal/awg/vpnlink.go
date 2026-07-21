package awg

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
)

// RenderVPNLink builds the native AmneziaVPN "vpn://" import link for a client.
// The app reads the top-level dns1 and applies it as the system DNS (overriding
// its built-in 1.1.1.1/8.8.8.8), and imports the embedded awg config.
//
// Wire format (matches the AmneziaVPN app): vpn:// + base64url(qCompress(json)),
// where qCompress is Qt's: 4-byte big-endian uncompressed length + zlib stream.
func RenderVPNLink(hub Hub, c Client, granted []Grant) string {
	conf := RenderClient(hub, c, granted)

	host, portStr := hub.Endpoint, ""
	if i := strings.LastIndex(hub.Endpoint, ":"); i >= 0 {
		host, portStr = hub.Endpoint[:i], hub.Endpoint[i+1:]
	}
	port, _ := strconv.Atoi(portStr)

	p := hub.Params
	lastConfig := map[string]any{
		"H1": u32(p.H1), "H2": u32(p.H2), "H3": u32(p.H3), "H4": u32(p.H4),
		"Jc": strconv.Itoa(p.Jc), "Jmin": strconv.Itoa(p.Jmin), "Jmax": strconv.Itoa(p.Jmax),
		"S1": strconv.Itoa(p.S1), "S2": strconv.Itoa(p.S2),
		"client_ip":       c.Address.String(),
		"client_priv_key": c.Keys.Private,
		"client_pub_key":  c.Keys.Public,
		"config":          conf,
		"hostName":        host,
		"port":            port,
		"server_pub_key":  hub.Keys.Public,
	}
	lastConfigJSON, _ := json.Marshal(lastConfig)

	// dns1 = dns2 = the client's resolver (hub tunnel IP when the resolver is on).
	// Both fields must be non-empty or AmneziaVPN keeps its built-in DNS; setting
	// both to the resolver overrides its defaults AND avoids a second DNS that
	// would bypass split-horizon.
	dns1 := clientDNS(hub, c, granted)
	if dns1 == "" {
		dns1 = "1.1.1.1"
	}
	dns2 := dns1

	root := map[string]any{
		"containers": []any{
			map[string]any{
				"awg": map[string]any{
					"isThirdPartyConfig": true,
					"last_config":        string(lastConfigJSON),
					"port":               portStr,
					"transport_proto":    "udp",
				},
				"container": "amnezia-awg",
			},
		},
		"defaultContainer": "amnezia-awg",
		"description":      c.Name,
		"dns1":             dns1,
		"dns2":             dns2,
		"hostName":         host,
	}
	rootJSON, _ := json.Marshal(root)

	return "vpn://" + base64.RawURLEncoding.EncodeToString(qCompress(rootJSON))
}

// clientDNS returns the primary DNS the client config uses (hub tunnel IP when
// the resolver is on).
func clientDNS(hub Hub, c Client, granted []Grant) string {
	if hub.Resolver {
		return hub.Address.String()
	}
	if c.DNS != "" {
		return c.DNS
	}
	for _, g := range granted {
		if g.NodeDNS != "" {
			return g.NodeDNS
		}
	}
	return hub.DNS
}

// qCompress mirrors Qt's qCompress: 4-byte big-endian original length, then a
// zlib stream. AmneziaVPN uses Qt's qUncompress to read it back.
func qCompress(data []byte) []byte {
	var buf bytes.Buffer
	// data is a marshalled client config (a few KB); its length always fits uint32.
	// #nosec G115 -- config payload length is far below math.MaxUint32.
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	_, _ = zw.Write(data)
	_ = zw.Close()
	return buf.Bytes()
}

func u32(v uint32) string { return strconv.FormatUint(uint64(v), 10) }
