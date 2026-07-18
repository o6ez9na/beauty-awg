package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"time"

	"beautifulwg/internal/awg"
	"beautifulwg/internal/store"

	"github.com/google/uuid"
)

// --- nodes ---

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.St.ListNodes(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hs := awg.LatestHandshakes(s.Svc.Applier.Iface)
	for i := range nodes {
		nodes[i].Online = peerOnline(hs, nodes[i].PublicKey)
	}
	writeJSON(w, http.StatusOK, nodes)
}

// peerOnline reports whether a peer handshaked recently enough to be considered up.
func peerOnline(hs map[string]int64, pubkey string) bool {
	if pubkey == "" {
		return false
	}
	last, ok := hs[pubkey]
	return ok && last > 0 && time.Now().Unix()-last < 180
}

type createNodeReq struct {
	Name     string   `json:"name"`
	LANIface string   `json:"lan_iface"`
	Subnets  []string `json:"subnets"`
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var req createNodeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.LANIface == "" || len(req.Subnets) == 0 {
		http.Error(w, "name, lan_iface and at least one subnet required", http.StatusBadRequest)
		return
	}
	subnets := make([]netip.Prefix, 0, len(req.Subnets))
	for _, s := range req.Subnets {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			http.Error(w, "bad subnet "+s, http.StatusBadRequest)
			return
		}
		subnets = append(subnets, p)
	}
	keys, err := awg.GenerateKeypair()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	psk, _ := awg.GeneratePresharedKey()
	id, addr, err := s.St.CreateNode(r.Context(), req.Name, req.LANIface, subnets, keys, psk)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id.String(), "address": addr.String()})
}

type updateNodeReq struct {
	DNS     *string   `json:"dns"`
	Domains *[]string `json:"domains"`
	Name    *string   `json:"name"`
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req updateNodeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		if *req.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if err := s.St.RenameNode(r.Context(), id, *req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if req.DNS != nil {
		if err := s.St.SetNodeDNS(r.Context(), id, *req.DNS); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if req.Domains != nil {
		if err := s.St.SetNodeDomains(r.Context(), id, *req.Domains); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.St.DeleteNode(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNodeConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	hub, node, reach, err := s.St.GetNodeForExport(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeConf(w, node.Name, awg.RenderNode(hub, node, reach))
}

// --- clients ---

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.St.ListClients(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hs := awg.LatestHandshakes(s.Svc.Applier.Iface)
	for i := range clients {
		clients[i].Online = peerOnline(hs, clients[i].PublicKey)
	}
	writeJSON(w, http.StatusOK, clients)
}

type createClientReq struct {
	Name string `json:"name"`
	DNS  string `json:"dns"`
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	var req createClientReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	keys, err := awg.GenerateKeypair()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// No PSK for clients: the AmneziaVPN "vpn://" format has no PSK field, so the
	// app connects without it — a PSK would break those clients. (.conf clients
	// work fine without it.)
	id, addr, err := s.St.CreateClient(r.Context(), req.Name, req.DNS, keys, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id.String(), "address": addr.String()})
}

type updateClientReq struct {
	Enabled *bool   `json:"enabled"`
	DNS     *string `json:"dns"`
	Name    *string `json:"name"`
}

func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req updateClientReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		if *req.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if err := s.St.RenameClient(r.Context(), id, *req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if req.Enabled != nil && req.DNS != nil {
		if err := s.St.UpdateClient(r.Context(), id, *req.Enabled, *req.DNS); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.St.DeleteClient(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	hub, client, grants, err := s.St.GetClientForExport(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeConf(w, client.Name, awg.RenderClient(hub, client, grants))
}

// handleClientVPNLink returns the native AmneziaVPN "vpn://" import link.
func (s *Server) handleClientVPNLink(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	hub, client, grants, err := s.St.GetClientForExport(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(awg.RenderVPNLink(hub, client, grants)))
}

// --- graph layout ---

func (s *Server) handleGetLayout(w http.ResponseWriter, r *http.Request) {
	positions, err := s.St.GetGraphLayout(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(positions))
}

func (s *Server) handleSetLayout(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !json.Valid(body) {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := s.St.SetGraphLayout(r.Context(), string(body)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- grants ---

func (s *Server) handleGrant(w http.ResponseWriter, r *http.Request) {
	cid, ok := pathUUID(w, r, "id")
	nid, ok2 := pathUUID(w, r, "nodeId")
	if !ok || !ok2 {
		return
	}
	if err := s.St.SetGrant(r.Context(), cid, nid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	cid, ok := pathUUID(w, r, "id")
	nid, ok2 := pathUUID(w, r, "nodeId")
	if !ok || !ok2 {
		return
	}
	if err := s.St.DeleteGrant(r.Context(), cid, nid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- node-to-node links (site-to-site) ---

func (s *Server) handleListNodeLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.St.ListNodeLinks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if links == nil {
		links = []store.NodeLinkDTO{}
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) handleLinkNode(w http.ResponseWriter, r *http.Request) {
	src, ok := pathUUID(w, r, "id")
	dst, ok2 := pathUUID(w, r, "dstId")
	if !ok || !ok2 {
		return
	}
	if err := s.St.SetNodeLink(r.Context(), src, dst); err != nil {
		// validation failures (self/hub/overlap) are client errors
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnlinkNode(w http.ResponseWriter, r *http.Request) {
	src, ok := pathUUID(w, r, "id")
	dst, ok2 := pathUUID(w, r, "dstId")
	if !ok || !ok2 {
		return
	}
	if err := s.St.DeleteNodeLink(r.Context(), src, dst); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- grant rules (access levels) ---

func (s *Server) handleGetGrantRules(w http.ResponseWriter, r *http.Request) {
	cid, ok := pathUUID(w, r, "id")
	nid, ok2 := pathUUID(w, r, "nodeId")
	if !ok || !ok2 {
		return
	}
	rules, err := s.St.GrantRules(r.Context(), cid, nid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) handleSetGrantRules(w http.ResponseWriter, r *http.Request) {
	cid, ok := pathUUID(w, r, "id")
	nid, ok2 := pathUUID(w, r, "nodeId")
	if !ok || !ok2 {
		return
	}
	var rules []store.RuleDTO
	if !decodeJSON(w, r, &rules) {
		return
	}
	if err := s.St.SetGrantRules(r.Context(), cid, nid, rules); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- grant internet-exit ---

func (s *Server) handleGetGrantExit(w http.ResponseWriter, r *http.Request) {
	cid, ok := pathUUID(w, r, "id")
	nid, ok2 := pathUUID(w, r, "nodeId")
	if !ok || !ok2 {
		return
	}
	exit, err := s.St.GrantExit(r.Context(), cid, nid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"exit": exit})
}

type setGrantExitReq struct {
	Exit bool `json:"exit"`
}

func (s *Server) handleSetGrantExit(w http.ResponseWriter, r *http.Request) {
	cid, ok := pathUUID(w, r, "id")
	nid, ok2 := pathUUID(w, r, "nodeId")
	if !ok || !ok2 {
		return
	}
	var req setGrantExitReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.St.SetGrantExit(r.Context(), cid, nid, req.Exit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- shared ---

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		http.Error(w, "bad "+name, http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

func writeConf(w http.ResponseWriter, name, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+".conf\"")
	_, _ = w.Write([]byte(body))
}
