// Typed client for the Go panel API. All requests are same-origin (/api/*),
// proxied to the backend by next.config rewrites, so the session cookie flows.

export interface Node {
  id: string;
  name: string;
  address: string;
  lan_iface: string;
  subnets: string[];
  status: "pending" | "active" | "rejected";
  hostname: string;
  last_seen: string | null;
  is_hub: boolean;
  dns: string;
  domains: string[];
  online: boolean;
}

export interface Client {
  id: string;
  name: string;
  address: string;
  dns: string;
  enabled: boolean;
  granted_nodes: string[];
  online: boolean;
}

export interface Rule {
  dest: string; // CIDR or host/32
  proto: "any" | "tcp" | "udp";
  port_from: number; // 0 = all ports
  port_to: number; // 0 = single (=port_from) or all
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text || res.statusText);
  }
  const ct = res.headers.get("content-type") || "";
  if (ct.includes("application/json")) return (await res.json()) as T;
  return undefined as T;
}

export const api = {
  login: (username: string, password: string) =>
    req<{ status: string }>("POST", "/api/login", { username, password }),
  logout: () => req<void>("POST", "/api/logout"),

  listNodes: () => req<Node[]>("GET", "/api/nodes"),
  createNode: (name: string, lan_iface: string, subnets: string[]) =>
    req<{ id: string; address: string }>("POST", "/api/nodes", { name, lan_iface, subnets }),
  deleteNode: (id: string) => req<void>("DELETE", `/api/nodes/${id}`),
  updateNode: (id: string, dns: string, domains: string[]) =>
    req<void>("PATCH", `/api/nodes/${id}`, { dns, domains }),
  renameNode: (id: string, name: string) =>
    req<void>("PATCH", `/api/nodes/${id}`, { name }),
  approveNode: (id: string) => req<void>("POST", `/api/nodes/${id}/approve`),
  rejectNode: (id: string) => req<void>("POST", `/api/nodes/${id}/reject`),

  listClients: () => req<Client[]>("GET", "/api/clients"),
  createClient: (name: string, dns: string) =>
    req<{ id: string; address: string }>("POST", "/api/clients", { name, dns }),
  updateClient: (id: string, enabled: boolean, dns: string) =>
    req<void>("PATCH", `/api/clients/${id}`, { enabled, dns }),
  renameClient: (id: string, name: string) =>
    req<void>("PATCH", `/api/clients/${id}`, { name }),
  deleteClient: (id: string) => req<void>("DELETE", `/api/clients/${id}`),

  grant: (clientId: string, nodeId: string) =>
    req<void>("PUT", `/api/clients/${clientId}/grants/${nodeId}`),
  revoke: (clientId: string, nodeId: string) =>
    req<void>("DELETE", `/api/clients/${clientId}/grants/${nodeId}`),

  getGrantRules: (clientId: string, nodeId: string) =>
    req<Rule[]>("GET", `/api/clients/${clientId}/grants/${nodeId}/rules`),
  setGrantRules: (clientId: string, nodeId: string, rules: Rule[]) =>
    req<void>("PUT", `/api/clients/${clientId}/grants/${nodeId}/rules`, rules),

  getGrantExit: (clientId: string, nodeId: string) =>
    req<{ exit: boolean }>("GET", `/api/clients/${clientId}/grants/${nodeId}/exit`),
  setGrantExit: (clientId: string, nodeId: string, exit: boolean) =>
    req<void>("PUT", `/api/clients/${clientId}/grants/${nodeId}/exit`, { exit }),

  // config endpoints return text/plain; consumed directly as URLs for download.
  getLayout: () => req<Record<string, { x: number; y: number }>>("GET", "/api/layout"),
  setLayout: (positions: Record<string, { x: number; y: number }>) =>
    req<void>("PUT", "/api/layout", positions),

  clientConfigUrl: (id: string) => `/api/clients/${id}/config`,
  clientVPNLinkUrl: (id: string) => `/api/clients/${id}/vpnlink`,
  nodeConfigUrl: (id: string) => `/api/nodes/${id}/config`,
  fetchText: async (url: string) => {
    const res = await fetch(url, { credentials: "same-origin" });
    if (!res.ok) throw new ApiError(res.status, await res.text());
    return res.text();
  },
};
