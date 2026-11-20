//go:build linux

/*
 * NXContainer Network Relay
 *
 * 在联网沙箱内监听代理端口，把连接交给宿主侧执行器，并运行用户命令。
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
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// 宿主侧执行器经 bwrap 传入的控制套接字（fd 3 为 seccomp 过滤器）。
const relayControlFD = 4

// NetRelay 运行在联网沙箱内：在沙箱回环上监听 HTTP 代理端口，把每个连接的描述符经控制套接字交给宿主侧执行器，
// 由它按公网策略代为连接；随后以代理环境变量运行用户命令，返回其退出码。沙箱网络命名空间里没有其他接口，
// 不经代理的连接无法离开沙箱；宿主侧会复核收到的描述符，所以沙箱内程序拿到控制套接字也无法扩大权限。
func NetRelay(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "nxcontainer：缺少要执行的命令")
		return 2
	}
	syscall.CloseOnExec(relayControlFD)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "nxcontainer：无法在沙箱回环上监听代理端口：", err)
		return 125
	}
	proxy := "http://127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			file, err := conn.(*net.TCPConn).File()
			conn.Close()
			if err != nil {
				continue
			}
			_ = syscall.Sendmsg(relayControlFD, []byte{0}, syscall.UnixRights(int(file.Fd())), nil, 0)
			file.Close()
		}
	}()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(),
		"HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "http_proxy="+proxy, "https_proxy="+proxy,
		"NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=localhost,127.0.0.1,::1", "NODE_USE_ENV_PROXY=1",
	)
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal())
			}
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "nxcontainer：无法启动命令：", err)
		return 127
	}
	return 0
}
