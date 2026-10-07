// chrome-front is the only container on both chrome-net (Chrome's own network) and
// code-docker-internal. Chrome sits on chrome-net alone, so a page it opens can't reach
// code-docker's nginx, dind's unauthenticated API or anything else on the internal
// network. chrome-front carries exactly two kinds of traffic across:
//
//   - code-docker -> Chrome's CDP: a plain TCP relay from chrome-cdp:9223 (this
//     container's alias on code-docker-internal) to cdp-bridge wrap inside Chrome's
//     container, which checks the bearer token.
//   - Chrome -> dev servers: Chrome runs with --host-resolver-rules "MAP localhost
//     chrome-front", so http://localhost:5173 in Chrome connects here, on chrome-net, port
//     5173. Each port in the forward table is relayed to its target (code-docker:5173,
//     dind:8080, ...). Ports not in the table aren't listened on at all.
//
// A forward is reachable by every page Chrome opens, not only by the dev tool it was
// added for, so targetPolicy limits where one may point: only at code-docker and dind
// (FRONT_TARGET_HOSTS), and never at the ports on them that are control planes rather
// than dev servers (FRONT_DENY_TARGETS). Both are checked by resolved address, when a
// forward is added and again on every connection.
//
// The forward table is managed over a small HTTP API, plus a page for people, served on
// code-docker-internal only. Serving it on chrome-net too would let any page Chrome opens
// add forwards through http://localhost:<api port>. webmanager shows the page as a
// provider page.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed page.html
var pageFS embed.FS

// Ports below 1024 would need CAP_NET_BIND_SERVICE, which this container drops.
const minPort, maxPort = 1024, 65535

type Forward struct {
	Port   int    `json:"port"`
	Target string `json:"target"`
	Note   string `json:"note,omitempty"`
}

type forwardStatus struct {
	Forward
	Listening   bool   `json:"listening"`
	Connections int    `json:"connections"`
	LastError   string `json:"lastError,omitempty"`
}

type activeForward struct {
	Forward
	listener  net.Listener
	conns     map[net.Conn]struct{}
	lastError string
}

type table struct {
	mu        sync.Mutex
	bindIP    string
	stateFile string
	policy    *targetPolicy
	forwards  map[int]*activeForward
}

var errUnresolved = errors.New("does not resolve")

// targetPolicy decides which addresses a forward may reach. It compares resolved
// addresses, not target strings, so an IP literal or another alias of a denied host
// is caught too.
type targetPolicy struct {
	hosts  []string            // names whose addresses may be targets
	deny   map[string][]string // host name -> ports never forwarded to
	lookup func(ctx context.Context, host string) ([]string, error)
}

func parsePolicy(hosts, deny string) (*targetPolicy, error) {
	p := &targetPolicy{hosts: strings.Fields(strings.ReplaceAll(hosts, ",", " ")), deny: map[string][]string{}, lookup: net.DefaultResolver.LookupHost}
	if len(p.hosts) == 0 {
		return nil, errors.New("FRONT_TARGET_HOSTS is empty: no forward could reach anything")
	}
	for _, d := range strings.Fields(strings.ReplaceAll(deny, ",", " ")) {
		host, port, err := net.SplitHostPort(d)
		n, perr := strconv.Atoi(port)
		if err != nil || perr != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("FRONT_DENY_TARGETS entry %q is not host:port", d)
		}
		if !slices.Contains(p.hosts, host) {
			return nil, fmt.Errorf("FRONT_DENY_TARGETS entry %q names a host that is not in FRONT_TARGET_HOSTS", d)
		}
		p.deny[host] = append(p.deny[host], strconv.Itoa(n))
	}
	return p, nil
}

// check returns the address to dial for target, or why it is refused. The caller dials
// exactly that address, so the name can't resolve differently between check and dial.
func (p *targetPolicy) check(ctx context.Context, target string) (string, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", err
	}
	addrs, err := p.lookup(ctx, host)
	if err != nil || len(addrs) == 0 {
		return "", fmt.Errorf("target host %s: %w", host, errUnresolved)
	}
	addr := addrs[0]
	var names []string
	for _, h := range p.hosts {
		if hostAddrs, err := p.lookup(ctx, h); err == nil && slices.Contains(hostAddrs, addr) {
			names = append(names, h)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("target %s is not on %s; a forward can only reach dev servers there", host, strings.Join(p.hosts, " or "))
	}
	for _, name := range names {
		if slices.Contains(p.deny[name], port) {
			return "", fmt.Errorf("%s:%s is never forwarded: every page Chrome opens could use it, and it is a control plane, not a dev server", name, port)
		}
	}
	return net.JoinHostPort(addr, port), nil
}

func (t *table) status() []forwardStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []forwardStatus{}
	for _, f := range t.forwards {
		out = append(out, forwardStatus{
			Forward:     f.Forward,
			Listening:   f.listener != nil,
			Connections: len(f.conns),
			LastError:   f.lastError,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func validate(f Forward) error {
	if f.Port < minPort || f.Port > maxPort {
		return fmt.Errorf("port must be %d-%d", minPort, maxPort)
	}
	if strings.Contains(f.Target, "://") {
		return errors.New("target must be host:port, not a URL")
	}
	host, port, err := net.SplitHostPort(f.Target)
	if err != nil || host == "" {
		return fmt.Errorf("target must be host:port, like code-docker:%d", f.Port)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return errors.New("target port must be 1-65535")
	}
	if strings.ContainsAny(host, "/ \t") {
		return errors.New("target host must be a hostname or IP")
	}
	if len(f.Note) > 200 {
		return errors.New("note must be at most 200 characters")
	}
	return nil
}

// set adds or replaces the forward for f.Port and starts listening for it.
func (t *table) set(f Forward) forwardStatus {
	t.mu.Lock()
	if old := t.forwards[f.Port]; old != nil {
		t.closeLocked(old)
	}
	af := &activeForward{Forward: f, conns: map[net.Conn]struct{}{}}
	t.forwards[f.Port] = af
	t.listenLocked(af)
	t.saveLocked()
	st := forwardStatus{Forward: f, Listening: af.listener != nil, LastError: af.lastError}
	t.mu.Unlock()
	if st.Listening {
		log.Printf("forward localhost:%d -> %s", f.Port, f.Target)
	} else {
		log.Printf("forward localhost:%d -> %s, not listening: %s", f.Port, f.Target, st.LastError)
	}
	return st
}

func (t *table) remove(port int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	af := t.forwards[port]
	if af == nil {
		return false
	}
	t.closeLocked(af)
	delete(t.forwards, port)
	t.saveLocked()
	log.Printf("forward localhost:%d removed", port)
	return true
}

func (t *table) closeLocked(af *activeForward) {
	if af.listener != nil {
		af.listener.Close()
		af.listener = nil
	}
	for c := range af.conns {
		c.Close()
	}
}

func (t *table) listenLocked(af *activeForward) {
	ln, err := net.Listen("tcp", net.JoinHostPort(t.bindIP, strconv.Itoa(af.Port)))
	if err != nil {
		af.lastError = err.Error()
		return
	}
	af.listener = ln
	go t.accept(af, ln)
}

func (t *table) accept(af *activeForward, ln net.Listener) {
	for {
		client, err := ln.Accept()
		if err != nil {
			return // closed by remove/set
		}
		t.mu.Lock()
		if af.listener != ln {
			t.mu.Unlock()
			client.Close()
			return
		}
		af.conns[client] = struct{}{}
		target := af.Target
		t.mu.Unlock()
		go t.relay(af, client, target)
	}
}

func (t *table) relay(af *activeForward, client net.Conn, target string) {
	defer func() {
		client.Close()
		t.mu.Lock()
		delete(af.conns, client)
		t.mu.Unlock()
	}()
	// Resolved and checked per connection, so a target container that was recreated
	// with a new IP keeps working, and a name that now points somewhere denied stops.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	addr, err := t.policy.check(ctx, target)
	cancel()
	var upstream net.Conn
	if err == nil {
		upstream, err = net.DialTimeout("tcp", addr, 5*time.Second)
	}
	if err != nil {
		t.mu.Lock()
		af.lastError = err.Error()
		t.mu.Unlock()
		return
	}
	defer upstream.Close()
	t.mu.Lock()
	af.lastError = ""
	t.mu.Unlock()
	pipe(client, upstream)
}

// pipe copies both ways until both directions are done, passing each half-close on.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		} else {
			dst.Close()
		}
	}
	wg.Add(2)
	go copyHalf(a, b)
	go copyHalf(b, a)
	wg.Wait()
}

func (t *table) saveLocked() {
	list := []Forward{}
	for _, f := range t.forwards {
		list = append(list, f.Forward)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Port < list[j].Port })
	data, _ := json.MarshalIndent(list, "", "  ")
	tmp := t.stateFile + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		log.Printf("WARNING: could not save %s: %v", t.stateFile, err)
		return
	}
	if err := os.Rename(tmp, t.stateFile); err != nil {
		log.Printf("WARNING: could not save %s: %v", t.stateFile, err)
	}
}

func (t *table) load() {
	data, err := os.ReadFile(t.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Printf("WARNING: could not read %s: %v - starting with no forwards", t.stateFile, err)
		return
	}
	var list []Forward
	if err := json.Unmarshal(data, &list); err != nil {
		log.Printf("WARNING: %s is not a forward list: %v - starting with no forwards", t.stateFile, err)
		return
	}
	for _, f := range list {
		if err := validate(f); err != nil {
			log.Printf("WARNING: skipping saved forward %d -> %s: %v", f.Port, f.Target, err)
			continue
		}
		// A target that doesn't resolve right now (dind still starting) is kept; the
		// per-connection check refuses it if it turns out to be denied.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := t.policy.check(ctx, f.Target)
		cancel()
		if err != nil && !errors.Is(err, errUnresolved) {
			log.Printf("WARNING: skipping saved forward %d -> %s: %v", f.Port, f.Target, err)
			continue
		}
		t.set(f)
	}
}

// relayCDP passes every connection on listen to upstream untouched; cdp-bridge wrap on
// the other end checks the token.
func relayCDP(listen, upstream string) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("CDP relay: %v", err)
	}
	log.Printf("CDP relay %s -> %s", listen, upstream)
	for {
		client, err := ln.Accept()
		if err != nil {
			log.Fatalf("CDP relay: %v", err)
		}
		go func() {
			defer client.Close()
			up, err := net.DialTimeout("tcp", upstream, 5*time.Second)
			if err != nil {
				log.Printf("CDP relay: %v", err)
				return
			}
			defer up.Close()
			pipe(client, up)
		}()
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func api(t *table) http.Handler {
	mux := http.NewServeMux()
	page, _ := pageFS.ReadFile("page.html")
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(page)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /api/forwards", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"forwards": t.status()})
	})
	mux.HandleFunc("POST /api/forwards", func(w http.ResponseWriter, r *http.Request) {
		var f Forward
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&f); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be {\"port\": 5173, \"target\": \"code-docker:5173\"}"})
			return
		}
		f.Target = strings.TrimSpace(f.Target)
		if f.Target == "" {
			f.Target = "code-docker:" + strconv.Itoa(f.Port)
		}
		if err := validate(f); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if _, err := t.policy.check(r.Context(), f.Target); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, t.set(f))
	})
	mux.HandleFunc("DELETE /api/forwards/{port}", func(w http.ResponseWriter, r *http.Request) {
		port, err := strconv.Atoi(r.PathValue("port"))
		if err != nil || !t.remove(port) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no forward on that port"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// resolveBind turns a network-qualified name (<container>.<network>) into this
// container's address on that network, waiting for Docker's DNS to know it.
func resolveBind(name string) string {
	deadline := time.Now().Add(60 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		addrs, err := net.DefaultResolver.LookupHost(ctx, name)
		cancel()
		if err == nil && len(addrs) > 0 {
			return addrs[0]
		}
		if time.Now().After(deadline) {
			// Fail closed: binding 0.0.0.0 instead would put the API on chrome-net, where
			// any page Chrome opens could reach it.
			log.Fatalf("could not resolve %s: %v", name, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("[chrome-front] ")
	apiHost := os.Getenv("FRONT_INTERNAL_NAME")
	forwardHost := os.Getenv("FRONT_CHROME_NAME")
	if apiHost == "" || forwardHost == "" {
		log.Fatal("FRONT_INTERNAL_NAME and FRONT_CHROME_NAME must name this container on code-docker-internal and chrome-net")
	}
	internalIP := resolveBind(apiHost)
	chromeIP := resolveBind(forwardHost)

	go relayCDP(net.JoinHostPort(internalIP, env("FRONT_CDP_PORT", "9223")), env("FRONT_CDP_UPSTREAM", "chrome-browser:9223"))

	targetHosts := env("FRONT_TARGET_HOSTS", "code-docker dind")
	denyTargets := env("FRONT_DENY_TARGETS", "code-docker:80 code-docker:82 dind:2375 dind:2376")
	policy, err := parsePolicy(targetHosts, denyTargets)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("forward targets: %s; never %s", targetHosts, denyTargets)

	t := &table{bindIP: chromeIP, stateFile: filepath.Join(env("FRONT_DATA_DIR", "/data"), "forwards.json"), policy: policy, forwards: map[int]*activeForward{}}
	t.load()

	listen := net.JoinHostPort(internalIP, env("FRONT_API_PORT", "8090"))
	log.Printf("forwards listen on %s (chrome-net); API and page on %s", chromeIP, listen)
	server := &http.Server{Addr: listen, Handler: api(t), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}
