package main

import (
	"context"
	"log"
	"net/http"
	"net/netip"
	"os"
	"strconv"

	"beautifulwg/internal/api"
	"beautifulwg/internal/awg"
	"beautifulwg/internal/config"
	"beautifulwg/internal/service"
	"beautifulwg/internal/store"
)

func main() {
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
	svc := &service.Service{St: st, Applier: applier}

	srv := &api.Server{
		St:            st,
		Svc:           svc,
		Secret:        cfg.SessionSecret,
		SecureCookies: os.Getenv("INSECURE_COOKIES") == "",
	}

	if err := api.BootstrapAdmin(ctx, srv, os.Getenv("ADMIN_USER"), os.Getenv("ADMIN_PASSWORD")); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}

	// Apply current DB state to the system once at boot.
	if err := svc.Reconcile(ctx); err != nil {
		log.Printf("initial reconcile failed (continuing): %v", err)
	}

	log.Printf("listening on %s (iface=%s dry_run=%v)", cfg.ListenAddr, cfg.AWGIface, cfg.DryRun)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Routes()); err != nil {
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
