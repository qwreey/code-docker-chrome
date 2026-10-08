package main

// `cdp-bridge close` asks the Chrome on a local DevTools port to shut down
// the way closing its last window does, and waits for it to go.
//
// chromium-service.sh runs it on SIGTERM instead of passing the signal on.
// Chrome exits on SIGTERM without flushing its cookie store, which it
// otherwise writes about every 30 s: a login completed in the half minute
// before a container restart was gone after it (measured). Browser.close
// takes the ordinary shutdown path, which writes everything first.

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

func closeMain(args []string) int {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	cdp := fs.String("cdp", "127.0.0.1:9222", "Chrome's DevTools address")
	timeout := fs.Duration("timeout", 8*time.Second, "how long to wait for Chrome to close")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := closeBrowser(*cdp, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "cdp-bridge close: %v\n", err)
		return 1
	}
	return 0
}

func closeBrowser(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/json/version")
	if err != nil {
		return err
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	err = json.NewDecoder(resp.Body).Decode(&version)
	resp.Body.Close()
	if err != nil {
		return err
	}
	u, err := url.Parse(version.WebSocketDebuggerURL)
	if err != nil || u.Scheme != "ws" {
		return fmt.Errorf("unexpected webSocketDebuggerUrl %q", version.WebSocketDebuggerURL)
	}

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(deadline)

	key := make([]byte, 16)
	rand.Read(key)
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		u.RequestURI(), addr, base64.StdEncoding.EncodeToString(key))
	br := bufio.NewReader(conn)
	hs, err := http.ReadResponse(br, nil)
	if err != nil {
		return err
	}
	if hs.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("websocket handshake: %s", hs.Status)
	}

	if _, err := conn.Write(maskedTextFrame([]byte(`{"id":1,"method":"Browser.close"}`))); err != nil {
		return err
	}
	// Chrome answers, then drops the connection once it is shutting down.
	// Reading until then is the wait.
	if _, err := io.Copy(io.Discard, br); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return errors.New("Chrome did not close in time")
		}
	}
	return nil
}

// maskedTextFrame is one final text frame, masked as a client must send it.
func maskedTextFrame(payload []byte) []byte {
	frame := []byte{0x81}
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	default:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	frame = append(frame, mask...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	return frame
}
