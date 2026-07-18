package api

import (
	"net/http"
	"net/netip"

	"beautifulwg/internal/awg"
)

// --- node-side enrollment (gated by the shared enroll secret, no cookie) ---

type enrollReq struct {
	Name      string   `json:"name"`
	Hostname  string   `json:"hostname"`
	LANIface  string   `json:"lan_iface"`
	PublicKey string   `json:"public_key"`
	Subnets   []string `json:"subnets"`
}

// handleEnroll registers a node as pending. No secret: the admin approves each
// request manually in the panel, which is the gate.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req enrollReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.PublicKey == "" || req.LANIface == "" || len(req.Subnets) == 0 {
		http.Error(w, "public_key, lan_iface and subnets required", http.StatusBadRequest)
		return
	}
	subnets := make([]netip.Prefix, 0, len(req.Subnets))
	for _, sn := range req.Subnets {
		p, err := netip.ParsePrefix(sn)
		if err != nil {
			http.Error(w, "bad subnet "+sn, http.StatusBadRequest)
			return
		}
		subnets = append(subnets, p)
	}
	name := req.Name
	if name == "" {
		name = req.Hostname
	}
	token, status, err := s.St.EnrollNode(r.Context(), name, req.Hostname, req.LANIface, req.PublicKey, subnets)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "status": status})
}

// handleEnrollPoll: the node polls this until status=active, then applies config.
func (s *Server) handleEnrollPoll(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	id, status, err := s.St.NodeByToken(r.Context(), token)
	if err != nil {
		http.Error(w, "unknown token", http.StatusNotFound)
		return
	}
	_ = s.St.TouchNodeByToken(r.Context(), token)

	resp := map[string]any{"status": status}
	if status == "active" {
		hub, node, reach, err := s.St.GetNodeForExport(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp["config"] = awg.RenderNode(hub, node, reach)
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- admin approve / reject ---

func (s *Server) handleApproveNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.St.ApproveNode(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRejectNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.St.RejectNode(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Svc.Reconcile(r.Context()); err != nil {
		http.Error(w, "reconcile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
