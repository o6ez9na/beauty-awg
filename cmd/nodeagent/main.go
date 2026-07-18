// nodeagent runs ON a home node. On first run it has no config: the web UI shows
// a single field to enter the panel's IP and a Connect button. Connecting sends
// an enrollment request to that panel; once the admin approves, the panel pushes
// the config and the agent applies it (config push over CGNAT via polling).
//
// The node's LAN subnet + interface are auto-detected. The private key is
// generated locally and never leaves the node (the panel sends a placeholder the
// agent substitutes).
//
// Env:
//   NODE_PASSWORD   HTTP Basic password for the web UI (user "admin")
//   NODE_LISTEN     web UI listen addr (default ":8088")
//   STATE_FILE      keypair+token+panel store (default /var/lib/awg-nodeagent/state.json)
//   AWG_IFACE       interface (default "awg0")
//   AWG_CONF        config path (default /etc/amnezia/amneziawg/awg0.conf)
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"6ers3rk/internal/awg"
)

//go:embed index.html
var indexHTML []byte

//go:embed logo.svg
var logoSVG []byte

//go:embed favicon.svg
var faviconSVG []byte

var (
	password  = os.Getenv("NODE_PASSWORD")
	listen    = env("NODE_LISTEN", ":8088")
	stateFile = env("STATE_FILE", "/var/lib/awg-nodeagent/state.json")
	iface     = env("AWG_IFACE", "awg0")
	confPath  = env("AWG_CONF", "/etc/amnezia/amneziawg/awg0.conf")
)

type state struct {
	Private string `json:"private"`
	Public  string `json:"public"`
	Token   string `json:"token"`
	Panel   string `json:"panel"` // normalized panel base URL
}

// agent holds mutable runtime state shared between the HTTP handlers and the
// polling loop.
type agent struct {
	mu      sync.Mutex
	st      state
	status  string // last poll result: "" | pending | active | rejected
	polling bool
}

var a = &agent{}

func main() {
	if st, err := loadState(); err == nil {
		a.st = st
	}
	// Resume polling if we've already connected to a panel.
	if a.st.Panel != "" && a.st.Token != "" {
		a.startPolling()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveIndex)
	mux.HandleFunc("GET /logo.svg", serveSVG(logoSVG))
	mux.HandleFunc("GET /favicon.svg", serveSVG(faviconSVG))
	mux.HandleFunc("GET /api/state", auth(getState))
	mux.HandleFunc("POST /api/connect", auth(connect))
	mux.HandleFunc("GET /api/config", auth(getConfig))
	mux.HandleFunc("POST /api/config", auth(applyConfig))
	mux.HandleFunc("GET /api/status", auth(getStatus))

	log.Printf("node agent on %s (iface=%s)", listen, iface)
	log.Fatal(http.ListenAndServe(listen, mux))
}

// ---------------- connect + enrollment ----------------

type stateResp struct {
	Enrolled bool   `json:"enrolled"`
	Status   string `json:"status"`
	Panel    string `json:"panel"`
	Subnet   string `json:"subnet"`
	Iface    string `json:"iface"`
}

func getState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	resp := stateResp{Enrolled: a.st.Token != "", Status: a.status, Panel: a.st.Panel}
	a.mu.Unlock()
	if ifc, sn, err := detectLAN(); err == nil {
		resp.Iface = ifc
		resp.Subnet = sn.String()
	}
	writeJSON(w, resp)
}

type connectReq struct {
	Panel string `json:"panel"`
}

func connect(w http.ResponseWriter, r *http.Request) {
	var req connectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	panelURL, err := normalizePanel(req.Panel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ifc, subnet, err := detectLAN()
	if err != nil {
		http.Error(w, "could not auto-detect LAN: "+err.Error(), http.StatusInternalServerError)
		return
	}

	a.mu.Lock()
	if a.st.Private == "" {
		kp, err := awg.GenerateKeypair()
		if err != nil {
			a.mu.Unlock()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		a.st.Private, a.st.Public = kp.Private, kp.Public
	}
	a.st.Panel = panelURL
	st := a.st
	a.mu.Unlock()

	if err := enroll(st, ifc, subnet); err != nil {
		http.Error(w, "enroll failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	a.startPolling()
	writeJSON(w, map[string]string{"status": "requested"})
}

func enroll(st state, lanIface string, subnet netip.Prefix) error {
	host, _ := os.Hostname()
	body, _ := json.Marshal(map[string]any{
		"name":       host,
		"hostname":   host,
		"lan_iface":  lanIface,
		"public_key": st.Public,
		"subnets":    []string{subnet.String()},
	})
	resp, err := http.Post(st.Panel+"/api/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("panel said %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var out struct{ Token, Status string }
	if err := json.Unmarshal(rb, &out); err != nil {
		return err
	}
	a.mu.Lock()
	a.st.Token = out.Token
	a.status = out.Status
	toSave := a.st
	a.mu.Unlock()
	if err := saveState(toSave); err != nil {
		return err
	}
	log.Printf("enrolled to %s: status=%s", st.Panel, out.Status)
	return nil
}

// startPolling launches the poll loop once.
func (a *agent) startPolling() {
	a.mu.Lock()
	if a.polling {
		a.mu.Unlock()
		return
	}
	a.polling = true
	a.mu.Unlock()
	go a.pollLoop()
}

func (a *agent) pollLoop() {
	var lastApplied string
	for {
		a.mu.Lock()
		panel, token, priv := a.st.Panel, a.st.Token, a.st.Private
		a.mu.Unlock()

		if panel != "" && token != "" {
			status, config, gone, err := poll(panel, token)
			switch {
			case gone:
				// The panel no longer knows this token: the node was deleted.
				// Tear the tunnel down, wipe the config, and reset to the
				// connect form.
				log.Printf("node removed from panel; tearing down")
				teardown()
				a.reset()
				lastApplied = ""
			case err != nil:
				log.Printf("poll: %v", err) // transient (panel down / network) — keep config
			default:
				a.mu.Lock()
				a.status = status
				a.mu.Unlock()
				if status == "active" && config != "" {
					full := strings.ReplaceAll(config, awg.NodePrivatePlaceholder, priv)
					if full != lastApplied {
						if err := writeAndApply(full); err != nil {
							log.Printf("apply pushed config: %v", err)
						} else {
							lastApplied = full
							log.Printf("applied config from panel")
						}
					}
				}
			}
		}
		time.Sleep(10 * time.Second)
	}
}

// poll returns gone=true when the panel reports the token is unknown (404),
// i.e. the node was deleted from the panel.
func poll(panel, token string) (status, config string, gone bool, err error) {
	resp, err := http.Get(panel + "/api/enroll/" + token)
	if err != nil {
		return "", "", false, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return "", "", true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", false, fmt.Errorf("panel said %d", resp.StatusCode)
	}
	var out struct{ Status, Config string }
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", "", false, err
	}
	return out.Status, out.Config, false, nil
}

// teardown brings the interface down and removes the local config.
func teardown() {
	exec.Command("awg-quick", "down", iface).Run()
	_ = os.Remove(confPath)
	_ = os.Remove(confPath + ".bak")
}

// reset clears enrollment so the web UI returns to the connect form. The keypair
// is kept (harmless) so a fresh connect reuses it.
func (a *agent) reset() {
	a.mu.Lock()
	a.st.Token = ""
	a.st.Panel = ""
	a.status = ""
	toSave := a.st
	a.mu.Unlock()
	_ = saveState(toSave)
}

// ---------------- LAN auto-detection ----------------

// detectLAN finds the primary interface (the one with the default route) and its
// IPv4 subnet, e.g. ("ens18", 192.168.1.0/24).
func detectLAN() (string, netip.Prefix, error) {
	out, err := exec.Command("ip", "-o", "route", "get", "1.1.1.1").Output()
	if err != nil {
		return "", netip.Prefix{}, err
	}
	fields := strings.Fields(string(out))
	var dev, src string
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "dev":
			dev = fields[i+1]
		case "src":
			src = fields[i+1]
		}
	}
	if dev == "" || src == "" {
		return "", netip.Prefix{}, fmt.Errorf("no default route")
	}
	srcAddr, err := netip.ParseAddr(src)
	if err != nil {
		return "", netip.Prefix{}, err
	}
	ifc, err := net.InterfaceByName(dev)
	if err != nil {
		return "", netip.Prefix{}, err
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.To4() == nil {
			continue
		}
		ones, _ := ipnet.Mask.Size()
		p := netip.PrefixFrom(srcAddr, ones).Masked()
		if p.Contains(srcAddr) {
			return dev, p, nil
		}
	}
	return "", netip.Prefix{}, fmt.Errorf("no IPv4 subnet on %s", dev)
}

// normalizePanel turns user input ("150.241.89.70", "1.2.3.4:3000",
// "http://host:3000") into a base URL. Defaults to http:// and port 3000.
func normalizePanel(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", fmt.Errorf("panel address required")
	}
	if !strings.Contains(in, "://") {
		in = "http://" + in
	}
	u, err := url.Parse(in)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid panel address")
	}
	if u.Port() == "" {
		u.Host = u.Host + ":3000"
	}
	return u.Scheme + "://" + u.Host, nil
}

// ---------------- config apply ----------------

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
		return fmt.Errorf("%s", out.String())
	}
	return nil
}

// ---------------- web UI (editor, shown once active) ----------------

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// serveSVG serves an embedded SVG asset (logo / favicon).
func serveSVG(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(b)
	}
}

func auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if password == "" {
			http.Error(w, "web UI disabled (no NODE_PASSWORD)", http.StatusServiceUnavailable)
			return
		}
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="6ers3rk node"`)
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

func loadState() (state, error) {
	var st state
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func saveState(st state) error {
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	return os.WriteFile(stateFile, b, 0o600)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
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

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
