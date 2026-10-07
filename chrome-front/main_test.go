package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func testPolicy(t *testing.T) *targetPolicy {
	t.Helper()
	p, err := parsePolicy("code-docker dind", "code-docker:80 code-docker:82 dind:2375 dind:2376")
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string][]string{
		"code-docker":   {"172.21.0.5"},
		"dind":          {"172.21.0.6"},
		"router":        {"172.21.0.2"},
		"alias-of-dind": {"172.21.0.6"},
		"localhost":     {"127.0.0.1"},
	}
	p.lookup = func(_ context.Context, host string) ([]string, error) {
		if a, ok := hosts[host]; ok {
			return a, nil
		}
		if strings.HasPrefix(host, "172.") || strings.HasPrefix(host, "127.") {
			return []string{host}, nil // the resolver returns IP literals as they are
		}
		return nil, errors.New("no such host")
	}
	return p
}

func TestPolicyAllowsDevServers(t *testing.T) {
	p := testPolicy(t)
	for target, want := range map[string]string{
		"code-docker:5173": "172.21.0.5:5173",
		"dind:8080":        "172.21.0.6:8080",
		"172.21.0.6:8080":  "172.21.0.6:8080",
		"code-docker:22":   "172.21.0.5:22",
	} {
		got, err := p.check(context.Background(), target)
		if err != nil || got != want {
			t.Errorf("check(%s) = %q, %v; want %q", target, got, err, want)
		}
	}
}

func TestPolicyRefuses(t *testing.T) {
	p := testPolicy(t)
	for _, target := range []string{
		"dind:2375", // the Docker API
		"dind:2376",
		"172.21.0.6:2375",    // the same, by address
		"alias-of-dind:2375", // the same, by another name
		"code-docker:80",     // nginx: code-server and webmanager
		"code-docker:82",     // WebDAV
		"router:80",          // not a target host at all
		"router:53",
		"localhost:8090", // chrome-front itself
		"172.21.0.9:8090",
	} {
		if addr, err := p.check(context.Background(), target); err == nil {
			t.Errorf("check(%s) = %q, want refused", target, addr)
		}
	}
}

func TestPolicyUnresolvedIsDistinct(t *testing.T) {
	p := testPolicy(t)
	if _, err := p.check(context.Background(), "nowhere:5173"); !errors.Is(err, errUnresolved) {
		t.Errorf("check(nowhere:5173) = %v, want errUnresolved", err)
	}
	if _, err := p.check(context.Background(), "dind:2375"); errors.Is(err, errUnresolved) {
		t.Errorf("a denied target must not read as unresolved")
	}
}

func TestParsePolicyRejectsBadConfig(t *testing.T) {
	for _, c := range [][2]string{
		{"", ""},
		{"code-docker", "router:80"},   // deny host not a target host
		{"code-docker", "code-docker"}, // no port
		{"code-docker", "code-docker:0"},
	} {
		if _, err := parsePolicy(c[0], c[1]); err == nil {
			t.Errorf("parsePolicy(%q, %q) succeeded", c[0], c[1])
		}
	}
}
