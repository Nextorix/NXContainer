/*
 * NXContainer Network Proxy
 *
 * 联网沙箱的宿主侧 HTTP 代理：代沙箱内程序连接公网，拒绝回环、私有网段与本机地址。
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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"
)

// 沙箱内程序经 HTTP_PROXY / HTTPS_PROXY 发来的请求由这里代为连接。
// 在真正发起连接时按解析出的 IP 检查，DNS 重绑定到内网地址同样被拒绝。
const (
	proxyMaxConnections = 64
	proxyHeaderTimeout  = 30 * time.Second
	proxyDialTimeout    = 30 * time.Second
)

var errBlockedDestination = errors.New("只允许访问公网地址")

// 公网之外的保留网段：共享地址、协议分配、文档、基准测试、保留与 IPv6 过渡地址。
var blockedPrefixes = func() []netip.Prefix {
	var prefixes []netip.Prefix
	for _, value := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4", "100::/64", "2001::/32", "2001:db8::/32", "2002::/16", "64:ff9b:1::/48", "fec0::/10",
	} {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}()

var nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsInterfaceLocalMulticast() || address.IsMulticast() {
		return false
	}
	// NAT64 地址按其中嵌入的 IPv4 地址判断。
	if nat64Prefix.Contains(address) {
		bytes := address.As16()
		return publicAddress(netip.AddrFrom4([4]byte(bytes[12:])))
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// 本机网卡上的地址（包括公网地址）同样拒绝，避免经本机地址访问只监听在宿主上的服务。
func localAddresses() map[netip.Addr]bool {
	local := map[netip.Addr]bool{}
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil {
			local[prefix.Addr().Unmap()] = true
		}
	}
	return local
}

type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

func publicDialer() dialFunc {
	local := localAddresses()
	dialer := &net.Dialer{Timeout: proxyDialTimeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !publicAddress(ip) || local[ip.Unmap()] {
			return errBlockedDestination
		}
		return nil
	}}
	return dialer.DialContext
}

type netProxy struct {
	dial   dialFunc
	ctx    context.Context
	cancel context.CancelFunc
	slots  chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
}

func newNetProxy(ctx context.Context, dial dialFunc) *netProxy {
	ctx, cancel := context.WithCancel(ctx)
	return &netProxy{dial: dial, ctx: ctx, cancel: cancel, slots: make(chan struct{}, proxyMaxConnections), conns: map[net.Conn]struct{}{}}
}

// 登记连接，代理关闭时统一断开；已关闭时返回 false。
func (p *netProxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		return false
	}
	for _, conn := range conns {
		p.conns[conn] = struct{}{}
	}
	return true
}

func (p *netProxy) untrack(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range conns {
		delete(p.conns, conn)
	}
}

// 接管一个沙箱内的客户端连接；超过并发上限或代理已关闭时直接断开。
func (p *netProxy) serve(client net.Conn) {
	select {
	case p.slots <- struct{}{}:
	default:
		client.Close()
		return
	}
	// 在锁内登记并计数：close 取消之后不会再有 wg.Add 与 wg.Wait 并发
	p.mu.Lock()
	if p.ctx.Err() != nil {
		p.mu.Unlock()
		<-p.slots
		client.Close()
		return
	}
	p.conns[client] = struct{}{}
	p.wg.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.wg.Done()
		defer func() { <-p.slots }()
		defer p.untrack(client)
		defer client.Close()
		p.handle(client)
	}()
}

func (p *netProxy) close() {
	p.mu.Lock()
	p.cancel()
	for conn := range p.conns {
		conn.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func proxyError(client net.Conn, status int, message string) {
	body := "nxcontainer：" + message + "\n"
	fmt.Fprintf(client, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, http.StatusText(status), len(body), body)
}

func dialError(client net.Conn, err error) {
	if errors.Is(err, errBlockedDestination) {
		proxyError(client, http.StatusForbidden, errBlockedDestination.Error())
		return
	}
	proxyError(client, http.StatusBadGateway, "无法连接目标："+err.Error())
}

// 逐跳头部不转发给目标。
var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func (p *netProxy) handle(client net.Conn) {
	_ = client.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
	reader := bufio.NewReader(client)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Time{})
	if request.Method == http.MethodConnect {
		if _, _, err := net.SplitHostPort(request.Host); err != nil {
			proxyError(client, http.StatusBadRequest, "CONNECT 目标必须是 主机:端口")
			return
		}
		upstream, err := p.dial(p.ctx, "tcp", request.Host)
		if err != nil {
			dialError(client, err)
			return
		}
		defer upstream.Close()
		if !p.track(upstream) {
			return
		}
		defer p.untrack(upstream)
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		tunnel(client, reader, upstream)
		return
	}
	// 普通 HTTP 请求为绝对地址形式；每个连接只转发一个请求，响应后关闭。
	if request.URL.Scheme != "http" || request.URL.Host == "" {
		proxyError(client, http.StatusBadRequest, "只支持 http:// 绝对地址或 CONNECT 隧道")
		return
	}
	target := request.URL.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(strings.Trim(target, "[]"), "80")
	}
	upstream, err := p.dial(p.ctx, "tcp", target)
	if err != nil {
		dialError(client, err)
		return
	}
	defer upstream.Close()
	if !p.track(upstream) {
		return
	}
	defer p.untrack(upstream)
	for _, name := range hopHeaders {
		request.Header.Del(name)
	}
	request.Close = true
	if err := request.Write(upstream); err != nil {
		proxyError(client, http.StatusBadGateway, "转发请求失败："+err.Error())
		return
	}
	response, err := http.ReadResponse(bufio.NewReader(upstream), request)
	if err != nil {
		proxyError(client, http.StatusBadGateway, "读取响应失败："+err.Error())
		return
	}
	defer response.Body.Close()
	response.Close = true
	_ = response.Write(client)
}

// 双向转发隧道数据；一侧结束时半关闭另一侧，两侧都结束后返回。
func tunnel(client net.Conn, buffered io.Reader, upstream net.Conn) {
	done := make(chan struct{}, 2)
	copyHalf := func(dst net.Conn, src io.Reader) {
		_, _ = io.Copy(dst, src)
		if half, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	go copyHalf(upstream, buffered)
	go copyHalf(client, upstream)
	<-done
	<-done
}
