# 6ers3rk

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

## Install via curl

One command on a fresh box; it asks whether to install the **panel** or a **node**:

```bash
curl -fsSL https://raw.githubusercontent.com/o6ez9na/beauty-awg/main/scripts/install.sh | sudo bash
```

Non-interactive:

```bash
curl -fsSL https://raw.githubusercontent.com/o6ez9na/beauty-awg/main/scripts/install.sh | sudo bash -s -- panel   # or: node
```

- **panel** installs AmneziaWG (module+tools), Docker, clones the repo to
  `/opt/6ers3rk`, generates `.env` (prompts for endpoint + admin password),
  enables IP forwarding, and runs `docker compose up -d --build`.
- **node** installs AmneziaWG, enables forwarding, asks you to paste the node
  `.conf` downloaded from the panel, and enables `awg-quick@awg0` at boot. It then
  optionally installs a **node web UI** (`awg-nodeagent`, systemd) to view/edit
  the node's awg config from a browser on the LAN (`http://<node-lan-ip>:8088`,
  user `admin`). Keep it LAN-only — it edits the config and runs `awg-quick` as root.

Replace `YOURUSER` with your GitHub repo (or set `REPO_URL=` env before running).

### Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/o6ez9na/beauty-awg/main/scripts/uninstall.sh | sudo bash
```

Removes only 6ers3rk's own bits and leaves shared things (Docker, the AmneziaWG
kernel module) alone. Flags: `PURGE_DATA=1` also deletes the DB volume / repo / node
keypair; `PURGE_AWG=1` also removes the kernel module (+ tools on a node); `FORCE=1`
skips the confirmation.

## Running the panel inside an LXC container

If the panel runs in an **LXC container** (Proxmox or plain LXC), `awg-quick`/
`amneziawg-go` fails with `/dev/net/tun does not exist` — the container has no TUN
device. This is required even for the userspace (`amneziawg-go`) path; userspace
only avoids the kernel module, not TUN.

Run this **on the LXC host** (not inside the container), as root. It bind-mounts
`/dev/net/tun` into the container, restarts it, and verifies:

```bash
# privileged container — simple run (asks for CTID if omitted):
curl -fsSL https://raw.githubusercontent.com/o6ez9na/beauty-awg/main/scripts/enable-tun-lxc.sh | sudo bash -s --
```

```bash
# unprivileged container — also relax AppArmor so TUN can be created:
curl -fsSL https://raw.githubusercontent.com/o6ez9na/beauty-awg/main/scripts/enable-tun-lxc.sh | sudo bash -s -- --apparmor=nesting
```

```bash
# privileged container with known CTID (no prompt):
curl -fsSL https://raw.githubusercontent.com/o6ez9na/beauty-awg/main/scripts/enable-tun-lxc.sh | sudo bash -s -- <CTID>
```

Replace `<CTID>` with your container ID (`pct list` / `lxc-ls -f`).

Flags: `--no-restart` (edit config only), `--docker` (print the TUN/`NET_ADMIN`
snippet for Docker running *inside* the LXC), `--apparmor=nesting|unconfined`
(unprivileged only; `nesting` keeps most confinement, `unconfined` removes
AppArmor entirely — less isolation). Without a flag the script never touches
AppArmor. It auto-detects Proxmox vs plain LXC, cgroup v1/v2, and privileged vs
unprivileged, and backs up the container config before editing.

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

- Panel UI: `http://<vps>:3000` (put a TLS reverse proxy in front for prod, then
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

### Site-to-site (node → node routing)

By default a node's LAN is reachable only by clients. To let a **whole LAN behind
one node reach a LAN behind another** (e.g. every device on `10.18.18.0/24` behind
node A reaches `192.168.1.0/24` behind node B) without installing a client on each
device, add a **node-to-node link** in the graph: drag an arrow from node A to
node B. A → B means "hosts on A's LAN may initiate to B's LAN"; the reverse
direction is a separate link (click the arrow → *Also allow B → A*).

This is **pure routing, no NAT** — real source IPs are preserved. Under the hood:

- each linked node carries the peer's subnets in its hub-peer `AllowedIPs` (added
  automatically to the rendered node config, so it routes cross-site traffic into
  the tunnel and WireGuard accepts the peer's source on the return path);
- the hub's nftables ACL gets one `accept` per src×dst subnet pair; the return
  path rides the existing `ct established` rule.

Requirements / notes:

- **Subnets must not overlap** between the two ends (no NAT to disambiguate). The
  API rejects overlapping links. `192.168.1.0/24` is a common default — make sure
  the two sites differ.
- The **hub-exit node cannot be part of a link** (it owns `0.0.0.0/0`).
- Both nodes' configs change when you add/remove a link. Nodes running the
  **node agent** re-pull and re-apply within ~10s automatically. For nodes whose
  `.conf` was installed by hand, **re-download and re-apply** both nodes' configs.
- On the **client-side router** (e.g. Keenetic), add a static route so LAN devices
  send the remote subnet to node A:
  `192.168.1.0/24 → <node-A LAN IP>` (Сеть → Маршрутизация).

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
