/*
 * NXContainer Network Proxy Tests
 *
 * 验证公网地址判定、CONNECT 隧道、普通 HTTP 转发与内网拒绝。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPublicAddress(t *testing.T) {
	for value, public := range map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true, "64:ff9b::808:808": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "0.1.2.3": false, "255.255.255.255": false, "224.0.0.1": false, "198.18.0.1": false,
		"::": false, "::1": false, "fe80::1": false, "fc00::1": false, "fd12::1": false, "::ffff:127.0.0.1": false,
		"::ffff:10.0.0.1": false, "64:ff9b::a00:1": false, "2001:db8::1": false, "2002:a00:1::": false, "ff02::1": false,
	} {
		if got := publicAddress(netip.MustParseAddr(value)); got != public {
			t.Errorf("%s 判定为公网=%v，应为 %v", value, got, public)
		}
	}
}

// 本地上游：回显请求行，用于验证转发内容。
func startUpstream(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "上游收到 %s %s %s", r.Method, r.Host, r.URL.RequestURI())
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return listener.Addr().String()
}

// 把一个本地 TCP 连接交给代理，返回客户端一侧。
func proxyClient(t *testing.T, proxy *netProxy) net.Conn {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	select {
	case conn := <-accepted:
		proxy.serve(conn)
	case <-time.After(5 * time.Second):
		t.Fatal("代理没有收到连接")
	}
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	return client
}

func exchange(t *testing.T, proxy *netProxy, request string) (*http.Response, string) {
	t.Helper()
	client := proxyClient(t, proxy)
	if _, err := io.WriteString(client, request); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

func TestNetProxyRejectsPrivateDestinations(t *testing.T) {
	upstream := startUpstream(t)
	_, port, _ := net.SplitHostPort(upstream)
	proxy := newNetProxy(context.Background(), publicDialer())
	defer proxy.close()
	for _, request := range []string{
		"CONNECT " + upstream + " HTTP/1.1\r\nHost: " + upstream + "\r\n\r\n",
		"CONNECT localhost:" + port + " HTTP/1.1\r\nHost: localhost:" + port + "\r\n\r\n",
		"GET http://" + upstream + "/secret HTTP/1.1\r\nHost: " + upstream + "\r\n\r\n",
		"GET http://169.254.169.254/latest/meta-data/ HTTP/1.1\r\nHost: 169.254.169.254\r\n\r\n",
	} {
		response, body := exchange(t, proxy, request)
		if response.StatusCode != http.StatusForbidden || strings.Contains(body, "上游收到") {
			t.Fatalf("%q 应被拒绝：%d %s", strings.SplitN(request, "\r\n", 2)[0], response.StatusCode, body)
		}
	}
}

func TestNetProxyForwardsAllowedDestinations(t *testing.T) {
	upstream := startUpstream(t)
	// 测试用拨号器放行本机上游，只验证代理协议本身。
	var dialer net.Dialer
	proxy := newNetProxy(context.Background(), dialer.DialContext)
	defer proxy.close()

	response, body := exchange(t, proxy, "GET http://"+upstream+"/path?q=1 HTTP/1.1\r\nHost: "+upstream+"\r\nProxy-Connection: keep-alive\r\n\r\n")
	if response.StatusCode != http.StatusOK || body != "上游收到 GET "+upstream+" /path?q=1" || !response.Close {
		t.Fatalf("普通 HTTP 转发错误：%d %q close=%v", response.StatusCode, body, response.Close)
	}

	client := proxyClient(t, proxy)
	reader := bufio.NewReader(client)
	fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upstream, upstream)
	established, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || established.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT 失败：%v %v", established, err)
	}
	fmt.Fprintf(client, "GET /tunnel HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", upstream)
	tunneled, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(tunneled.Body)
	if string(data) != "上游收到 GET "+upstream+" /tunnel" {
		t.Fatalf("隧道内容错误：%q", data)
	}

	response, _ = exchange(t, proxy, "GET https://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("https 绝对地址应要求使用 CONNECT：%d", response.StatusCode)
	}
}

func TestNetProxyCloseDisconnectsTunnels(t *testing.T) {
	upstream := startUpstream(t)
	var dialer net.Dialer
	proxy := newNetProxy(context.Background(), dialer.DialContext)
	client := proxyClient(t, proxy)
	reader := bufio.NewReader(client)
	fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upstream, upstream)
	if response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect}); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT 失败：%v %v", response, err)
	}
	closed := make(chan struct{})
	go func() { proxy.close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("关闭代理时隧道没有断开")
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("代理关闭后隧道仍然可用")
	}
}
