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
// One consequence of passing Host through: Chrome's DevTools HTTP endpoint has
// DNS-rebinding protection and answers "Host header is specified and is not an
// IP address or localhost" to anything else. Reaching wrap directly by service
// name therefore fails even with a valid token, while the supported path -
// through unwrap on 127.0.0.1 - always sends a Host Chrome accepts. Debug with
// `curl -H 'Host: 127.0.0.1:9222'` if you must talk to wrap by hand.
package main

import (
	"crypto/subtle"
	"flag"
	"log"
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
	listen := flag.String("listen", "", "address to listen on")
	upstream := flag.String("upstream", "", "host:port to forward to")
	tokenEnv := flag.String("token-env", "CHROME_CDP_TOKEN", "env var holding the shared secret")
	flag.Parse()

	if mode != "wrap" && mode != "unwrap" {
		log.Fatal("usage: cdp-bridge {wrap|unwrap} -listen ADDR -upstream HOST:PORT")
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
		h = requireBearer(token, proxy)
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
