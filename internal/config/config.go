package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL   string
	ListenAddr    string
	AWGIface      string
	ConfDir       string
	NFTFile       string
	DryRun        bool
	SessionSecret string
	ResolverListen string // e.g. "10.8.0.1:53"; empty disables the resolver
	DNSUpstream    string // default upstream, e.g. "1.1.1.1:53"
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:   env("DATABASE_URL", ""),
		ListenAddr:    env("LISTEN_ADDR", ":8080"),
		AWGIface:      env("AWG_IFACE", "awg0"),
		ConfDir:       env("AWG_CONF_DIR", "/etc/amnezia/amneziawg"),
		NFTFile:       env("AWG_NFT_FILE", "/etc/awgpanel/acl.nft"),
		DryRun:         env("AWG_DRY_RUN", "") != "",
		SessionSecret:  env("SESSION_SECRET", ""),
		ResolverListen: env("RESOLVER_LISTEN", ""),
		DNSUpstream:    env("DNS_UPSTREAM", "1.1.1.1:53"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if c.SessionSecret == "" {
		return c, fmt.Errorf("SESSION_SECRET is required")
	}
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
