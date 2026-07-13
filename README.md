# beautifulwg

Web panel to manage **AmneziaWG** users, nodes and per-user access. Routes a
device's traffic into home LANs that sit behind CGNAT, via a VPS relay.

## Topology

```
[client] --awg--> [VPS hub, white IP] <--awg-- [home node behind CGNAT]
  spoke              relay (this panel)            spoke, owns LAN 192.168.1.0/24
```

- The **hub** (VPS) has the public IP. It is the only inbound listener.
- **Home nodes** dial OUT to the hub (`PersistentKeepalive=25`) — no port-forward
  needed. Their config is static: install once, never touch again.
- The panel runs on the hub. Adding/removing a client only rewrites the hub's
  awg config + nftables ACL; nodes are untouched.
- **Access control** is enforced by nftables on the hub (default-drop forward +
  one `accept` per grant). Client `AllowedIPs` is the split-tunnel second layer.
- Node subnets must not overlap (WireGuard cryptokey routing picks the node by
  its `AllowedIPs`).

## Host prerequisites (VPS hub)

The `backend` container runs in the host network namespace with `NET_ADMIN` so it
can manage `awg0` + nftables on the host. The host must provide:

1. **AmneziaWG kernel module** (or `amneziawg-go` userspace). Install per
   https://github.com/amnezia-vpn/amneziawg-linux-kernel-module .
2. IP forwarding:
   ```
   sysctl -w net.ipv4.ip_forward=1   # persist in /etc/sysctl.d/
   ```
3. UDP `HUB_LISTEN_PORT` (default 51820) open in the provider firewall.

## Quickstart (Docker)

```bash
cp .env.example .env
# edit .env: SESSION_SECRET (openssl rand -hex 32), HUB_ENDPOINT, ADMIN_PASSWORD
docker compose up -d --build
```

- Panel UI: `http://<vps>:3000`  (put a TLS reverse proxy in front for prod, then
  set `INSECURE_COOKIES=` empty).
- Backend API: `http://<vps>:8080` (proxied by the frontend at `/api`).

On first boot the hub keypair + obfuscation params + the first admin are created.
The first mutation brings `awg0` up automatically (`awg-quick up`), then every
change hot-syncs with `awg syncconf` (existing tunnels are not dropped).

### Adding a node

1. In the UI, add a node (name, LAN iface, subnets). Download its `.conf`.
2. On the home server (AmneziaWG installed), place it at
   `/etc/amnezia/amneziawg/awg0.conf` and `awg-quick up awg0` (enable at boot).
3. Grant clients access to the node with the chips in the Clients table.

## Development (no Docker)

```bash
# Postgres
docker run -d --rm --name pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=awg -p 5432:5432 postgres:17-alpine

# Backend (dry-run: renders configs to stdout instead of touching the system)
DATABASE_URL='postgres://postgres:pw@localhost:5432/awg?sslmode=disable' \
SESSION_SECRET=dev AWG_DRY_RUN=1 INSECURE_COOKIES=1 \
ADMIN_USER=admin ADMIN_PASSWORD=admin HUB_ENDPOINT=203.0.113.10:51820 LISTEN_ADDR=:8099 \
go run ./cmd/panel

# Frontend
cd web && BACKEND_URL=http://localhost:8099 npm run dev
```

## Environment

See `.env.example`. Backend also honours `AWG_IFACE`, `AWG_CONF_DIR`,
`AWG_NFT_FILE`, `AWG_DRY_RUN`.
