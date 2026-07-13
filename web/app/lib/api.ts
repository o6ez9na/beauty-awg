// Typed client for the Go panel API. All requests are same-origin (/api/*),
// proxied to the backend by next.config rewrites, so the session cookie flows.

export interface Node {
  id: string;
  name: string;
  address: string;
  lan_iface: string;
  subnets: string[];
}

export interface Client {
  id: string;
  name: string;
  address: string;
  dns: string;
  enabled: boolean;
  granted_nodes: string[];
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

  listClients: () => req<Client[]>("GET", "/api/clients"),
  createClient: (name: string, dns: string) =>
    req<{ id: string; address: string }>("POST", "/api/clients", { name, dns }),
  updateClient: (id: string, enabled: boolean, dns: string) =>
    req<void>("PATCH", `/api/clients/${id}`, { enabled, dns }),
  deleteClient: (id: string) => req<void>("DELETE", `/api/clients/${id}`),

  grant: (clientId: string, nodeId: string) =>
    req<void>("PUT", `/api/clients/${clientId}/grants/${nodeId}`),
  revoke: (clientId: string, nodeId: string) =>
    req<void>("DELETE", `/api/clients/${clientId}/grants/${nodeId}`),

  // config endpoints return text/plain; consumed directly as URLs for download.
  clientConfigUrl: (id: string) => `/api/clients/${id}/config`,
  nodeConfigUrl: (id: string) => `/api/nodes/${id}/config`,
  fetchText: async (url: string) => {
    const res = await fetch(url, { credentials: "same-origin" });
    if (!res.ok) throw new ApiError(res.status, await res.text());
    return res.text();
  },
};
