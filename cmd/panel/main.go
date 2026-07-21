package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"time"

	"6ers3rk/internal/api"
	"6ers3rk/internal/awg"
	"6ers3rk/internal/buildversion"
	"6ers3rk/internal/config"
	"6ers3rk/internal/release"
	"6ers3rk/internal/resolver"
	"6ers3rk/internal/service"
	"6ers3rk/internal/store"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z" (see the
// Dockerfile's VERSION build arg and .github/workflows/release.yml). Left as
// "dev" for a source build; buildversion.Resolve then falls back to the git
// commit at startup.
var version = "dev"

func main() {
	version = buildversion.Resolve(version)
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	if err := ensureHub(ctx, st); err != nil {
		log.Fatalf("ensure hub: %v", err)
	}
	if err := st.SetEnrollSecretIfEmpty(ctx, os.Getenv("HUB_ENROLL_SECRET")); err != nil {
		log.Fatalf("set enroll secret: %v", err)
	}
	if err := st.SetWanIface(ctx, os.Getenv("HUB_WAN_IFACE")); err != nil {
		log.Fatalf("set wan iface: %v", err)
	}
	if err := st.EnsureHubNode(ctx); err != nil {
		log.Fatalf("ensure hub node: %v", err)
	}

	applier := awg.Applier{
		Iface:   cfg.AWGIface,
		ConfDir: cfg.ConfDir,
		NFTFile: cfg.NFTFile,
		DryRun:  cfg.DryRun,
	}
	svc := &service.Service{St: st, Applier: applier, Upstream: cfg.DNSUpstream}

	// Optional split-horizon DNS resolver. It listens on a local port; nft
	// redirects client :53 (to the hub tunnel IP) here, sidestepping anything
	// else already bound to :53 on the host.
	if cfg.ResolverListen != "" && !cfg.DryRun {
		if _, portStr, err := net.SplitHostPort(cfg.ResolverListen); err == nil {
			if p, err := strconv.Atoi(portStr); err == nil {
				st.ResolverPort = p
			}
		}
		res := resolver.New()
		res.Serve(cfg.ResolverListen)
		svc.Resolver = res
		st.ResolverOn = true
		log.Printf("split-horizon DNS resolver on %s (upstream %s)", cfg.ResolverListen, cfg.DNSUpstream)
	}

	srv := &api.Server{
		St:            st,
		Svc:           svc,
		Secret:        cfg.SessionSecret,
		SecureCookies: os.Getenv("INSECURE_COOKIES") == "",
		Release:       release.NewChecker(),
		Version:       version,
	}

	if err := api.BootstrapAdmin(ctx, srv, os.Getenv("ADMIN_USER"), os.Getenv("ADMIN_PASSWORD")); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}

	// Apply current DB state to the system once at boot.
	if err := svc.Reconcile(ctx); err != nil {
		log.Printf("initial reconcile failed (continuing): %v", err)
	}

	log.Printf("listening on %s (iface=%s dry_run=%v)", cfg.ListenAddr, cfg.AWGIface, cfg.DryRun)
	// ReadHeaderTimeout guards against slow-header (Slowloris) clients. TLS is
	// terminated by the reverse proxy in front of the panel, so plain HTTP here
	// is intentional.
	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// nosemgrep: go.lang.security.audit.net.use-tls.use-tls
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// ensureHub creates the singleton hub row from HUB_* env on first boot.
func ensureHub(ctx context.Context, st *store.Store) error {
	endpoint := os.Getenv("HUB_ENDPOINT")
	addrStr := envDefault("HUB_ADDRESS", "10.8.0.1")
	poolStr := envDefault("HUB_POOL_CIDR", "10.8.0.0/24")
	portStr := envDefault("HUB_LISTEN_PORT", "51820")
	dns := os.Getenv("HUB_DNS")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(addrStr)
	if err != nil {
		return err
	}
	pool, err := netip.ParsePrefix(poolStr)
	if err != nil {
		return err
	}
	created, err := st.EnsureHub(ctx, endpoint, port, addr, pool, dns)
	if err != nil {
		return err
	}
	if created {
		if endpoint == "" {
			log.Printf("WARNING: hub created with empty HUB_ENDPOINT; set it and update the hub row")
		}
		log.Printf("hub bootstrapped: %s pool=%s", addr, pool)
	}
	return nil
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
