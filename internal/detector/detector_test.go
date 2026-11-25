package detector

import (
	"net"
	"net/http"
	"testing"
)

func TestIsHTTPProxyPortDistinguishesHTTPFromSocks(t *testing.T) {
	// 1. Start a mock HTTP server that speaks HTTP
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpLn.Close()
	go func() {
		_ = http.Serve(httpLn, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.WriteHeader(http.StatusBadGateway)
		}))
	}()
	httpPort := httpLn.Addr().(*net.TCPAddr).Port

	reach, speaksHTTP := isHTTPProxyPort("127.0.0.1", httpPort)
	if !reach || !speaksHTTP {
		t.Fatalf("expected HTTP listener (%d) to report reach=true, speaksHTTP=true; got %v, %v", httpPort, reach, speaksHTTP)
	}

	// 2. Start a mock SOCKS5-only listener that rejects non-SOCKS5 bytes
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()
	go func() {
		for {
			c, err := socksLn.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte{0x05, 0xFF})
			_ = c.Close()
		}
	}()
	socksPort := socksLn.Addr().(*net.TCPAddr).Port

	reachSocks, speaksHTTPSocks := isHTTPProxyPort("127.0.0.1", socksPort)
	if !reachSocks || speaksHTTPSocks {
		t.Fatalf("expected SOCKS-only listener (%d) to report reach=true, speaksHTTP=false; got %v, %v", socksPort, reachSocks, speaksHTTPSocks)
	}
}

func TestSelectActiveClientAutoPriority(t *testing.T) {
	clients := []ProxyClientInfo{
		{ID: "clash_verge", Installed: true, Running: false, PortReachable: false},
		{ID: "clash_mi", Installed: false, Running: false},
		{ID: "v2rayn", Installed: true, Running: true, PortReachable: true, HTTPVerified: true, MixedPort: 10808},
	}
	picked := selectActiveClient("auto", clients, 0)
	if picked.ID != "v2rayn" {
		t.Fatalf("expected running v2rayn to be auto-selected, got %s", picked.ID)
	}

	forced := selectActiveClient("clash_verge", clients, 0)
	if forced.ID != "clash_verge" {
		t.Fatalf("expected forced clash_verge, got %s", forced.ID)
	}
}
