package api

import (
	"encoding/json"
	"net/http"

	"beautifulwg/internal/service"
	"beautifulwg/internal/store"
)

// Server holds dependencies for HTTP handlers.
type Server struct {
	St            *store.Store
	Svc           *service.Service
	Secret        string
	SecureCookies bool
}

// Routes wires the mux. Auth-protected routes go through requireAuth.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	// nodes
	mux.HandleFunc("GET /api/nodes", s.requireAuth(s.handleListNodes))
	mux.HandleFunc("POST /api/nodes", s.requireAuth(s.handleCreateNode))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.requireAuth(s.handleDeleteNode))
	mux.HandleFunc("GET /api/nodes/{id}/config", s.requireAuth(s.handleNodeConfig))

	// clients
	mux.HandleFunc("GET /api/clients", s.requireAuth(s.handleListClients))
	mux.HandleFunc("POST /api/clients", s.requireAuth(s.handleCreateClient))
	mux.HandleFunc("PATCH /api/clients/{id}", s.requireAuth(s.handleUpdateClient))
	mux.HandleFunc("DELETE /api/clients/{id}", s.requireAuth(s.handleDeleteClient))
	mux.HandleFunc("GET /api/clients/{id}/config", s.requireAuth(s.handleClientConfig))

	// grants
	mux.HandleFunc("PUT /api/clients/{id}/grants/{nodeId}", s.requireAuth(s.handleGrant))
	mux.HandleFunc("DELETE /api/clients/{id}/grants/{nodeId}", s.requireAuth(s.handleRevoke))

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
