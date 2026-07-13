// nodeagent runs ON a home node. Two roles, both optional:
//
//  1. Enrollment + push (when PANEL_URL is set): generates a local keypair,
//     announces itself to the panel, waits for admin approval, then pulls its
//     awg config and keeps it in sync (config push over CGNAT via polling). The
//     private key never leaves the node — the panel sends a placeholder that the
//     agent substitutes locally.
//  2. Web editor (when NODE_PASSWORD is set): a LAN browser UI to view/edit the
//     awg config and re-apply it.
//
// Env:
//   PANEL_URL       panel base URL, e.g. http://1.2.3.4:3000 (enables enrollment)
//   ENROLL_SECRET   shared secret required to enroll
//   NODE_NAME       display name (default: hostname)
//   LAN_IFACE       LAN interface to expose (default: eth0)
//   SUBNETS         comma-separated LAN subnets, e.g. 192.168.1.0/24
//   STATE_FILE      keypair+token store (default /var/lib/awg-nodeagent/state.json)
//   NODE_PASSWORD   HTTP Basic password for the web editor (user "admin")
//   NODE_LISTEN     web editor listen addr (default ":8088")
//   AWG_IFACE       interface (default "awg0")
//   AWG_CONF        config path (default /etc/amnezia/amneziawg/awg0.conf)
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"beautifulwg/internal/awg"
)

//go:embed index.html
var indexHTML []byte

var (
	panelURL     = strings.TrimRight(os.Getenv("PANEL_URL"), "/")
	enrollSecret = os.Getenv("ENROLL_SECRET")
	nodeName     = os.Getenv("NODE_NAME")
	lanIface     = env("LAN_IFACE", "eth0")
	subnetsEnv   = os.Getenv("SUBNETS")
	stateFile    = env("STATE_FILE", "/var/lib/awg-nodeagent/state.json")
	password     = os.Getenv("NODE_PASSWORD")
	listen       = env("NODE_LISTEN", ":8088")
	iface        = env("AWG_IFACE", "awg0")
	confPath     = env("AWG_CONF", "/etc/amnezia/amneziawg/awg0.conf")
)

type state struct {
	Private string `json:"private"`
	Public  string `json:"public"`
	Token   string `json:"token"`
}

func main() {
	if panelURL != "" {
		go enrollLoop()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveIndex)
	mux.HandleFunc("GET /api/config", auth(getConfig))
	mux.HandleFunc("POST /api/config", auth(applyConfig))
	mux.HandleFunc("GET /api/status", auth(getStatus))

	log.Printf("node agent on %s (iface=%s panel=%q)", listen, iface, panelURL)
	log.Fatal(http.ListenAndServe(listen, mux))
}

// ---------------- enrollment + push ----------------

func enrollLoop() {
	st, err := loadOrInitState()
	if err != nil {
		log.Printf("state init failed: %v", err)
		return
	}

	// Enroll (idempotent on the panel side; safe to repeat). Updates st.Token.
	if err := enroll(&st); err != nil {
		log.Printf("enroll request failed: %v (will keep polling)", err)
	}

	var lastApplied string
	for {
		status, config, err := poll(st.Token)
		if err != nil {
			log.Printf("poll: %v", err)
		} else {
			switch status {
			case "active":
				full := strings.ReplaceAll(config, awg.NodePrivatePlaceholder, st.Private)
				if full != lastApplied {
					if err := writeAndApply(full); err != nil {
						log.Printf("apply pushed config: %v", err)
					} else {
						lastApplied = full
						log.Printf("applied config from panel")
					}
				}
			case "pending":
				// waiting for admin approval
			case "rejected":
				log.Printf("enrollment rejected by admin")
			}
		}
		time.Sleep(15 * time.Second)
	}
}

func loadOrInitState() (state, error) {
	var st state
	if b, err := os.ReadFile(stateFile); err == nil {
		if json.Unmarshal(b, &st) == nil && st.Private != "" {
			return st, nil
		}
	}
	kp, err := awg.GenerateKeypair()
	if err != nil {
		return st, err
	}
	st = state{Private: kp.Private, Public: kp.Public}
	return st, saveState(st)
}

func saveState(st state) error {
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	return os.WriteFile(stateFile, b, 0o600)
}

func enroll(st *state) error {
	host, _ := os.Hostname()
	name := nodeName
	if name == "" {
		name = host
	}
	subnets := splitCSV(subnetsEnv)
	body, _ := json.Marshal(map[string]any{
		"secret":     enrollSecret,
		"name":       name,
		"hostname":   host,
		"lan_iface":  lanIface,
		"public_key": st.Public,
		"subnets":    subnets,
	})
	resp, err := http.Post(panelURL+"/api/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return &httpErr{resp.StatusCode, string(rb)}
	}
	var out struct{ Token, Status string }
	if err := json.Unmarshal(rb, &out); err != nil {
		return err
	}
	if out.Token != "" && out.Token != st.Token {
		st.Token = out.Token
		if err := saveState(*st); err != nil {
			return err
		}
	}
	log.Printf("enrolled: status=%s", out.Status)
	return nil
}

func poll(token string) (status, config string, err error) {
	if token == "" {
		return "", "", &httpErr{0, "no token yet"}
	}
	resp, err := http.Get(panelURL + "/api/enroll/" + token)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", &httpErr{resp.StatusCode, string(rb)}
	}
	var out struct{ Status, Config string }
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", "", err
	}
	return out.Status, out.Config, nil
}

// writeAndApply writes the config and reloads the interface (down+up so PostUp
// masquerade takes effect), restoring the previous config on failure.
func writeAndApply(config string) error {
	if old, err := os.ReadFile(confPath); err == nil {
		_ = os.WriteFile(confPath+".bak", old, 0o600)
	}
	if err := os.MkdirAll(filepath.Dir(confPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(confPath, []byte(config), 0o600); err != nil {
		return err
	}
	var out strings.Builder
	run(&out, "awg-quick", "down", iface)
	if code := run(&out, "awg-quick", "up", iface); code != 0 {
		if bak, err := os.ReadFile(confPath + ".bak"); err == nil {
			_ = os.WriteFile(confPath, bak, 0o600)
			run(&out, "awg-quick", "up", iface)
		}
		return &httpErr{0, out.String()}
	}
	return nil
}

// ---------------- web editor ----------------

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if password == "" {
			http.Error(w, "web editor disabled (no NODE_PASSWORD)", http.StatusServiceUnavailable)
			return
		}
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

func applyConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.Contains(string(body), "[Interface]") {
		http.Error(w, "config missing [Interface] section; not saved", http.StatusBadRequest)
		return
	}
	if err := writeAndApply(string(body)); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	var out strings.Builder
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

// ---------------- helpers ----------------

type httpErr struct {
	code int
	msg  string
}

func (e *httpErr) Error() string {
	if e.code == 0 {
		return e.msg
	}
	return http.StatusText(e.code) + ": " + e.msg
}

func run(out *strings.Builder, name string, args ...string) int {
	out.WriteString("$ " + name + " " + strings.Join(args, " ") + "\n")
	b, err := exec.CommandContext(context.Background(), name, args...).CombinedOutput()
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

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
