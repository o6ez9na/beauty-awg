package api

import (
	"encoding/json"
	"net/http"

	"6ers3rk/internal/release"
	"6ers3rk/internal/service"
	"6ers3rk/internal/store"
)

// Server holds dependencies for HTTP handlers.
type Server struct {
	St            *store.Store
	Svc           *service.Service
	Secret        string
	SecureCookies bool
	Release       *release.Checker // nil disables nodeagent update notices
}

// Routes wires the mux. Auth-protected routes go through requireAuth.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	// node enrollment (secret-gated, called by node agents — no admin cookie)
	mux.HandleFunc("POST /api/enroll", s.handleEnroll)
	mux.HandleFunc("GET /api/enroll/{token}", s.handleEnrollPoll)

	// nodes
	mux.HandleFunc("GET /api/nodes", s.requireAuth(s.handleListNodes))
	mux.HandleFunc("POST /api/nodes", s.requireAuth(s.handleCreateNode))
	mux.HandleFunc("PATCH /api/nodes/{id}", s.requireAuth(s.handleUpdateNode))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.requireAuth(s.handleDeleteNode))
	mux.HandleFunc("GET /api/nodes/{id}/config", s.requireAuth(s.handleNodeConfig))
	mux.HandleFunc("POST /api/nodes/{id}/approve", s.requireAuth(s.handleApproveNode))
	mux.HandleFunc("POST /api/nodes/{id}/reject", s.requireAuth(s.handleRejectNode))

	// node-to-node links (site-to-site routing between spokes)
	mux.HandleFunc("GET /api/nodes/links", s.requireAuth(s.handleListNodeLinks))
	mux.HandleFunc("PUT /api/nodes/{id}/links/{dstId}", s.requireAuth(s.handleLinkNode))
	mux.HandleFunc("DELETE /api/nodes/{id}/links/{dstId}", s.requireAuth(s.handleUnlinkNode))

	// clients
	mux.HandleFunc("GET /api/clients", s.requireAuth(s.handleListClients))
	mux.HandleFunc("POST /api/clients", s.requireAuth(s.handleCreateClient))
	mux.HandleFunc("PATCH /api/clients/{id}", s.requireAuth(s.handleUpdateClient))
	mux.HandleFunc("DELETE /api/clients/{id}", s.requireAuth(s.handleDeleteClient))
	mux.HandleFunc("GET /api/clients/{id}/config", s.requireAuth(s.handleClientConfig))
	mux.HandleFunc("GET /api/clients/{id}/vpnlink", s.requireAuth(s.handleClientVPNLink))

	// graph layout (saved node positions)
	mux.HandleFunc("GET /api/layout", s.requireAuth(s.handleGetLayout))
	mux.HandleFunc("PUT /api/layout", s.requireAuth(s.handleSetLayout))

	// grants
	mux.HandleFunc("PUT /api/clients/{id}/grants/{nodeId}", s.requireAuth(s.handleGrant))
	mux.HandleFunc("DELETE /api/clients/{id}/grants/{nodeId}", s.requireAuth(s.handleRevoke))

	// grant rules (access levels: subnets + ports)
	mux.HandleFunc("GET /api/clients/{id}/grants/{nodeId}/rules", s.requireAuth(s.handleGetGrantRules))
	mux.HandleFunc("PUT /api/clients/{id}/grants/{nodeId}/rules", s.requireAuth(s.handleSetGrantRules))

	// grant internet-exit (route the client's whole traffic out this node's WAN)
	mux.HandleFunc("GET /api/clients/{id}/grants/{nodeId}/exit", s.requireAuth(s.handleGetGrantExit))
	mux.HandleFunc("PUT /api/clients/{id}/grants/{nodeId}/exit", s.requireAuth(s.handleSetGrantExit))

	return mux
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}
