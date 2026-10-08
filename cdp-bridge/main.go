// cdp-bridge — carries Chrome DevTools Protocol across a container boundary
// with a shared-secret gate, in two halves:
//
//	wrap    runs next to Chrome. Listens on the container network, requires
//	        Authorization: Bearer <token>, strips it, forwards to Chrome's
//	        loopback-only debugging port.
//	unwrap  runs next to the MCP client. Listens on loopback, attaches the
//	        Bearer header, forwards to wrap.
//
// The point of the pair is that the Host header survives end to end. Chrome
// rewrites webSocketDebuggerUrl to whatever Host it was asked with, so a
// client that reaches unwrap at 127.0.0.1:9222 gets back
// ws://127.0.0.1:9222/devtools/... — pointing at unwrap again. CDP therefore
// looks entirely local to the client and needs no remote-aware flags.
//
// CDP has no authentication of its own: reaching the port is full control of
// the browser (cookie theft, arbitrary JS). The token is what lets this ride
// on a shared internal network instead of demanding a dedicated one.
//
// The same token also carries the screen. wrap answers GET /.vnc with
// "Upgrade: rfb" by splicing the connection onto wayvnc, and unwrap's
// -vnc-listen serves that as a plain VNC port on loopback, so an agent sees and
// drives the same desktop a person sees through router's VNC tab (see
// `cdp-bridge screen`, screen.go). Through the token rather than by joining the
// VNC network: that network also has Chrome on it, and a page Chrome opens could
// then reach code-docker directly.
//
// One consequence of passing Host through: Chrome's DevTools HTTP endpoint has
// DNS-rebinding protection and answers "Host header is specified and is not an
// IP address or localhost" to anything else. Reaching wrap directly by service
// name therefore fails even with a valid token, while the supported path -
// through unwrap on 127.0.0.1 - always sends a Host Chrome accepts. Debug with
// `curl -H 'Host: 127.0.0.1:9222'` if you must talk to wrap by hand.
package main

import (
	"bufio"
	"crypto/subtle"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	// Split as two statements, not one tuple assignment: Go does not order
	// an index expression against a function call in the same RHS, so
	// `mode, os.Args = os.Args[1], append(os.Args[:1], os.Args[2:]...)`
	// can run the append first and read the already-shifted slot.
	mode := ""
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		mode = os.Args[1]
		os.Args = append(os.Args[:1:1], os.Args[2:]...)
	}
	if mode == "screen" {
		os.Exit(screenMain(os.Args[1:]))
	}
	if mode == "close" {
		os.Exit(closeMain(os.Args[1:]))
	}
	listen := flag.String("listen", "", "address to listen on")
	upstream := flag.String("upstream", "", "host:port to forward to")
	tokenEnv := flag.String("token-env", "CHROME_CDP_TOKEN", "env var holding the shared secret")
	vncUpstream := flag.String("vnc-upstream", "", "wrap: wayvnc's host:port, served to token holders at "+vncPath+" (empty: off)")
	vncListen := flag.String("vnc-listen", "", "unwrap: loopback address to serve the screen on as plain VNC (empty: off)")
	flag.Parse()

	if mode != "wrap" && mode != "unwrap" {
		log.Fatal("usage: cdp-bridge {wrap|unwrap} -listen ADDR -upstream HOST:PORT, or cdp-bridge screen ...")
	}
	if *listen == "" || *upstream == "" {
		log.Fatal("cdp-bridge: -listen and -upstream are both required")
	}
	token := os.Getenv(*tokenEnv)
	if token == "" {
		// Fail closed. An empty token in wrap would accept every caller; in
		// unwrap it would send a blank header that wrap rejects with a 401
		// whose cause is invisible (the header is present, just empty) —
		// the exact failure roblox-studio-docker's overlay comments describe
		// for MCP_TOKEN.
		log.Fatalf("cdp-bridge: %s is empty — refusing to start", *tokenEnv)
	}

	target := &url.URL{Scheme: "http", Host: *upstream}
	proxy := &httputil.ReverseProxy{
		// Deliberately not NewSingleHostReverseProxy: that one rewrites the
		// path and leaves Host handling implicit. Here only the destination
		// changes — r.Out.Host is left exactly as the client sent it, which
		// is the whole mechanism (see package comment).
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = target.Scheme
			r.Out.URL.Host = target.Host
			r.Out.Host = r.In.Host
			if mode == "wrap" {
				// Chrome never sees the secret.
				r.Out.Header.Del("Authorization")
			} else {
				r.Out.Header.Set("Authorization", "Bearer "+token)
			}
		},
		// CDP is a long-lived duplex stream; buffering would stall it.
		// ReverseProxy handles the 101 upgrade and hijacks the connection
		// itself, so websockets need no special-casing beyond this.
		FlushInterval: -1,
		ErrorLog:      log.New(os.Stderr, "cdp-bridge: ", log.LstdFlags),
	}

	h := http.Handler(proxy)
	if mode == "wrap" {
		h = requireBearer(token, serveVNC(*vncUpstream, proxy))
	}
	if mode == "unwrap" && *vncListen != "" {
		go listenVNC(*vncListen, *upstream, token)
	}

	srv := &http.Server{
		Addr:    *listen,
		Handler: h,
		// No WriteTimeout/IdleTimeout: both would cut live CDP sessions.
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("cdp-bridge %s: %s -> %s", mode, *listen, *upstream)
	log.Fatal(srv.ListenAndServe())
}

func requireBearer(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const vncPath = "/.vnc"

// serveVNC hands GET /.vnc (already past requireBearer) to wayvnc and everything else
// to next. The path can't collide with Chrome's own endpoints, which all live under
// /json and /devtools.
func serveVNC(upstream string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != vncPath {
			next.ServeHTTP(w, r)
			return
		}
		if upstream == "" {
			http.Error(w, "the screen is not shared with agents here (CHROME_AGENT_VNC=false)", http.StatusNotFound)
			return
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "rfb") {
			http.Error(w, "expected Upgrade: rfb", http.StatusBadRequest)
			return
		}
		vnc, err := net.DialTimeout("tcp", upstream, 5*time.Second)
		if err != nil {
			http.Error(w, "wayvnc unreachable: "+err.Error(), http.StatusBadGateway)
			return
		}
		client, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			vnc.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: rfb\r\nConnection: Upgrade\r\n\r\n")
		if err := rw.Flush(); err != nil {
			client.Close()
			vnc.Close()
			return
		}
		pipe(bufConn{client, rw.Reader}, vnc)
	})
}

// listenVNC serves the screen as plain VNC on listen (loopback): each connection is
// upgraded at wrap's /.vnc with the token, then passed through untouched.
func listenVNC(listen, upstream, token string) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("cdp-bridge unwrap: VNC: %v", err)
	}
	log.Printf("cdp-bridge unwrap: VNC %s -> %s%s", listen, upstream, vncPath)
	for {
		client, err := ln.Accept()
		if err != nil {
			log.Fatalf("cdp-bridge unwrap: VNC: %v", err)
		}
		go func() {
			defer client.Close()
			up, err := net.DialTimeout("tcp", upstream, 5*time.Second)
			if err != nil {
				log.Printf("cdp-bridge unwrap: VNC: %v", err)
				return
			}
			defer up.Close()
			fmt.Fprintf(up, "GET %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nConnection: Upgrade\r\nUpgrade: rfb\r\n\r\n", vncPath, upstream, token)
			br := bufio.NewReader(up)
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				log.Printf("cdp-bridge unwrap: VNC: %v", err)
				return
			}
			if resp.StatusCode != http.StatusSwitchingProtocols {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				log.Printf("cdp-bridge unwrap: VNC refused: %s %s", resp.Status, strings.TrimSpace(string(body)))
				return
			}
			pipe(client, bufConn{up, br})
		}()
	}
}

// bufConn reads through a buffered reader that may already hold bytes past the HTTP
// exchange (the server speaks first in RFB), and writes to the connection itself.
type bufConn struct {
	net.Conn
	r io.Reader
}

func (c bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// pipe copies both ways until both directions are done, passing each half-close on.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	half := func(dst, src net.Conn) {
		io.Copy(dst, src)
		closeWrite(dst)
		done <- struct{}{}
	}
	go half(a, b)
	go half(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

func closeWrite(c net.Conn) {
	if bc, ok := c.(bufConn); ok {
		c = bc.Conn
	}
	if tc, ok := c.(interface{ CloseWrite() error }); ok {
		tc.CloseWrite()
		return
	}
	c.Close()
}
