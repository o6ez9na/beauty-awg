// nodeagent is a tiny single-binary web UI that runs ON a home node. It lets you
// view/edit the node's awg config and re-apply it, from a browser on the LAN.
//
// It is intentionally standalone (embedded HTML, no DB, no Node runtime) so it
// can run on a small home box. Bind it to the LAN and protect with a password.
//
// Env:
//   NODE_PASSWORD   required; HTTP Basic password (user is "admin")
//   NODE_LISTEN     listen addr, default ":8088"
//   AWG_IFACE       interface, default "awg0"
//   AWG_CONF        config path, default "/etc/amnezia/amneziawg/awg0.conf"
package main

import (
	"crypto/subtle"
	_ "embed"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed index.html
var indexHTML []byte

var (
	iface    = env("AWG_IFACE", "awg0")
	confPath = env("AWG_CONF", "/etc/amnezia/amneziawg/awg0.conf")
	password = os.Getenv("NODE_PASSWORD")
	listen   = env("NODE_LISTEN", ":8088")
)

func main() {
	if password == "" {
		log.Fatal("NODE_PASSWORD is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveIndex)
	mux.HandleFunc("GET /api/config", auth(getConfig))
	mux.HandleFunc("POST /api/config", auth(applyConfig))
	mux.HandleFunc("GET /api/status", auth(getStatus))

	log.Printf("node agent on %s (iface=%s conf=%s)", listen, iface, confPath)
	log.Fatal(http.ListenAndServe(listen, mux))
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// auth is HTTP Basic middleware. The browser prompts on 401.
func auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="beautifulwg node"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func getConfig(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(confPath)
	if err != nil {
		http.Error(w, "read config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(b)
}

// applyConfig writes the posted config then reloads the interface. It brings the
// interface fully down+up (not syncconf) so PostUp/masquerade changes take effect.
func applyConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	text := string(body)
	if !strings.Contains(text, "[Interface]") {
		http.Error(w, "config missing [Interface] section; not saved", http.StatusBadRequest)
		return
	}

	// Back up the current config so a bad edit is recoverable.
	if old, err := os.ReadFile(confPath); err == nil {
		_ = os.WriteFile(confPath+".bak", old, 0o600)
	}
	if err := os.MkdirAll(filepath.Dir(confPath), 0o700); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(confPath, body, 0o600); err != nil {
		http.Error(w, "write config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var out strings.Builder
	run(&out, "awg-quick", "down", iface) // ignore error: may already be down
	if code := run(&out, "awg-quick", "up", iface); code != 0 {
		out.WriteString("\n>>> awg-quick up FAILED; restoring backup\n")
		if bak, err := os.ReadFile(confPath + ".bak"); err == nil {
			_ = os.WriteFile(confPath, bak, 0o600)
			run(&out, "awg-quick", "up", iface)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(out.String()))
		return
	}
	run(&out, "awg", "show", iface)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(out.String()))
}

func getStatus(w http.ResponseWriter, r *http.Request) {
	var out strings.Builder
	run(&out, "awg", "show", iface)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(out.String()))
}

// run executes a command, appending a labelled block of its output, returns exit code.
func run(out *strings.Builder, name string, args ...string) int {
	out.WriteString("$ " + name + " " + strings.Join(args, " ") + "\n")
	b, err := exec.Command(name, args...).CombinedOutput()
	out.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		out.WriteByte('\n')
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		out.WriteString("(error: " + err.Error() + ")\n")
		return 1
	}
	return 0
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
