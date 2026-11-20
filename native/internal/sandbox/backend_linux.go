//go:build linux

/*
 * NXContainer Linux Backend
 *
 * 使用 bubblewrap、namespace 与 seccomp 建立原生隔离执行环境。
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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// 首版只使用非 setuid 的发行版 bubblewrap，不从 Agent 的 PATH 查找执行器。
func findBubblewrap(ctx context.Context) (string, error) {
	if os.Geteuid() == 0 {
		return "", problem("BACKEND_UNAVAILABLE", "实验后端拒绝以 root 运行，请使用普通账户")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", problem("BACKEND_UNAVAILABLE", "当前只支持 Linux amd64/arm64")
	}
	for _, candidate := range []string{"/usr/bin/bwrap", "/bin/bwrap"} {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() || info.Mode()&0111 == 0 || info.Mode()&os.ModeSetuid != 0 || info.Mode().Perm()&0022 != 0 {
			return "", problem("BACKEND_UNAVAILABLE", "bubblewrap 必须是不可被组或其他用户写入的非 setuid 可执行文件")
		}
		cmd := exec.CommandContext(ctx, candidate, "--help")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
		output, err := cmd.Output()
		if err != nil {
			return "", problem("BACKEND_UNAVAILABLE", "无法查询 bubblewrap 能力")
		}
		for _, flag := range []string{"--disable-userns", "--assert-userns-disabled", "--seccomp", "--size"} {
			if !bytes.Contains(output, []byte(flag)) {
				return "", problem("BACKEND_UNAVAILABLE", "bubblewrap 缺少必要能力："+flag)
			}
		}
		return candidate, nil
	}
	return "", problem("BACKEND_UNAVAILABLE", "未安装 bubblewrap，请通过系统包管理器安装")
}

func Probe(ctx context.Context) Capabilities {
	c := Capabilities{Platform: "linux", Architecture: runtime.GOARCH, Backend: "bubblewrap", Status: "experimental", Validation: validationStatus(runtime.GOOS, runtime.GOARCH)}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := probeCommand(ctx, Config{AgentID: "probe"}); err != nil {
		c.Reason = err.Error()
		return c
	}
	c.Exec, c.Files, c.ReadOnlyMounts, c.PrivateFilesystem, c.NetworkNone = true, true, true, true, true
	// 联网沙箱只多一个沙箱内的网络中继：能在沙箱回环上监听并运行命令即视为支持。
	c.NetworkInternet = probeCommand(ctx, Config{AgentID: "probe", Network: "internet"}) == nil
	return c
}

func probeCommand(ctx context.Context, config Config) error {
	cmd, control, err := buildCommand(ctx, config, Command{Argv: []string{"/usr/bin/true"}})
	if err != nil {
		return err
	}
	defer closeExtraFiles(cmd)
	if control != nil {
		defer control.Close()
	}
	var output bytes.Buffer
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("隔离自检失败：%s", strings.TrimSpace(output.String()))
	}
	return nil
}

func systemArguments() ([]string, error) {
	args := []string{}
	for _, name := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64"} {
		info, err := os.Lstat(name)
		if os.IsNotExist(err) && name != "/usr" {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			if err != nil {
				return nil, err
			}
			resolved := target
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(name), target)
			}
			if !contained("/usr", resolved) {
				return nil, problem("BACKEND_UNAVAILABLE", "系统目录链接不指向 /usr，当前布局不受支持")
			}
			args = append(args, "--symlink", target, name)
		} else {
			args = append(args, "--ro-bind", name, name)
		}
	}
	args = append(args, "--dir", "/etc")
	if info, err := os.Lstat("/etc/ld.so.cache"); err == nil && info.Mode().IsRegular() {
		args = append(args, "--ro-bind", "/etc/ld.so.cache", "/etc/ld.so.cache")
	}
	return args, nil
}

// 联网沙箱只读挂载系统 CA 证书，HTTPS 客户端才能校验证书；不挂载 /etc/ssl 中的其他内容（如私钥目录）。
func certificateArguments() []string {
	var args []string
	for _, name := range []string{"/etc/ssl/certs", "/etc/ssl/cert.pem", "/etc/pki/tls/certs", "/etc/pki/tls/cert.pem", "/etc/pki/ca-trust/extracted"} {
		if _, err := os.Stat(name); err == nil {
			args = append(args, "--ro-bind", name, name)
		}
	}
	return args
}

func protectedLinuxFilesystem(fsType int64) bool {
	// 文件系统魔数按低 32 位比较，不依赖内核如何扩展最高位。
	switch fsType & 0xffffffff {
	case 0x9fa0, 0x62656572, 0x27e0eb, 0x63677270, 0x1cd1, 0x19800202, 0x64626720, 0x74726163, 0xcafe4a11, 0x73636673:
		return true
	}
	return false
}

// 防止通过授权接口把宿主 /proc 等魔术文件系统重新带进隔离视图。
func validateMountSources(c Config, worker string) error {
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return problem("BACKEND_UNAVAILABLE", "无法检查宿主挂载信息")
	}
	for _, grant := range c.Mounts {
		for _, protected := range []string{"/proc", "/sys", "/dev", "/run"} {
			if contained(protected, grant.Source) || contained(grant.Source, protected) {
				return problem("PERMISSION_DENIED", "不能共享宿主设备、内核或运行控制目录")
			}
		}
		if grant.Access == "read-write" {
			for _, protected := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", worker} {
				if contained(protected, grant.Source) || contained(grant.Source, protected) {
					return problem("PERMISSION_DENIED", "不能授予系统或执行器目录写权限")
				}
			}
		}
		var fs syscall.Statfs_t
		if err := syscall.Statfs(grant.Source, &fs); err != nil {
			return err
		}
		if protectedLinuxFilesystem(int64(fs.Type)) {
			return problem("PERMISSION_DENIED", "不能挂载内核接口文件系统")
		}
		for _, line := range strings.Split(string(mountInfo), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 6 {
				continue
			}
			point, err := strconv.Unquote(`"` + fields[4] + `"`)
			if err != nil {
				return problem("BACKEND_UNAVAILABLE", "无法解析宿主挂载路径")
			}
			if point != grant.Source && contained(grant.Source, point) {
				return problem("PERMISSION_DENIED", "共享目录不能包含嵌套挂载")
			}
		}
	}
	return nil
}

func validatePlatform(c Config) error {
	if c.Limits.MemoryBytes != 0 || c.Limits.MaxProcesses != 0 || c.Limits.CPUPercent != 0 {
		return problem("UNSUPPORTED_CAPABILITY", "Linux 尚未配置可验证的 cgroup 配额，不能忽略所请求的限制")
	}
	worker, err := os.Executable()
	if err != nil {
		return err
	}
	return validateMountSources(c, worker)
}

func runPlatform(ctx context.Context, c Config, command Command, stdout, stderr io.Writer) (int, error) {
	if err := validatePlatform(c); err != nil {
		return -1, err
	}
	cmd, control, err := buildCommand(ctx, c, command)
	if err != nil {
		return -1, err
	}
	defer closeExtraFiles(cmd)
	if control != nil {
		defer control.Close()
	}
	cmd.Stdin = strings.NewReader(command.Stdin)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	// 父进程不保留交给沙箱的描述符：沙箱内的中继退出后，控制套接字随之结束。
	closeExtraFiles(cmd)
	if control != nil {
		proxy := newNetProxy(ctx, publicDialer())
		defer proxy.close()
		go serveRelay(control, proxy)
	}
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// 接收沙箱内中继交来的客户端连接，交给宿主侧代理；控制套接字结束或关闭时返回。
func serveRelay(control *net.UnixConn, proxy *netProxy) {
	buffer := make([]byte, 16)
	oob := make([]byte, syscall.CmsgSpace(4*8))
	for {
		n, oobn, _, _, err := control.ReadMsgUnix(buffer, oob)
		if oobn > 0 {
			if messages, parseErr := syscall.ParseSocketControlMessage(oob[:oobn]); parseErr == nil {
				for i := range messages {
					fds, rightsErr := syscall.ParseUnixRights(&messages[i])
					if rightsErr != nil {
						continue
					}
					for _, fd := range fds {
						acceptRelayed(fd, proxy)
					}
				}
			}
		}
		if err != nil || (n == 0 && oobn == 0) {
			return
		}
	}
}

// 只接受 IPv4 / IPv6 的 TCP 流式套接字（中继接受的客户端连接），其他描述符直接关闭。
func acceptRelayed(fd int, proxy *netProxy) {
	file := os.NewFile(uintptr(fd), "nx-sandbox-client")
	defer file.Close()
	kind, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE)
	if err != nil || kind != syscall.SOCK_STREAM {
		return
	}
	domain, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_DOMAIN)
	if err != nil || (domain != syscall.AF_INET && domain != syscall.AF_INET6) {
		return
	}
	conn, err := net.FileConn(file)
	if err != nil {
		return
	}
	proxy.serve(conn)
}

// 中继与宿主侧执行器之间的控制套接字：SEQPACKET 保留消息边界，每条消息携带一个连接的描述符。
func relayControlPair() (*net.UnixConn, *os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_SEQPACKET|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	host := os.NewFile(uintptr(fds[0]), "nx-control")
	conn, err := net.FileConn(host)
	host.Close()
	if err != nil {
		syscall.Close(fds[1])
		return nil, nil, err
	}
	return conn.(*net.UnixConn), os.NewFile(uintptr(fds[1]), "nx-relay"), nil
}

// 联网沙箱（文件接口除外）经沙箱内的网络中继运行用户命令，control 为宿主侧的控制套接字，其余情况为 nil。
func buildCommand(ctx context.Context, c Config, command Command) (*exec.Cmd, *net.UnixConn, error) {
	worker, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	if err := validateMountSources(c, worker); err != nil {
		return nil, nil, err
	}
	bwrap, err := findBubblewrap(ctx)
	if err != nil {
		return nil, nil, err
	}
	system, err := systemArguments()
	if err != nil {
		return nil, nil, err
	}
	network := c.Network == "internet" && len(command.Argv) > 0 && command.Argv[0] != "/nx/worker"
	args := []string{
		"--unshare-user", "--unshare-pid", "--unshare-net", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup",
		"--disable-userns", "--assert-userns-disabled", "--cap-drop", "ALL", "--new-session", "--die-with-parent", "--clearenv",
	}
	args = append(args, system...)
	if network {
		args = append(args, certificateArguments()...)
	}
	args = append(args,
		"--proc", "/proc", "--remount-ro", "/proc", "--dev", "/dev",
		"--size", "16777216", "--tmpfs", "/dev/shm", "--remount-ro", "/dev",
		"--size", "67108864", "--tmpfs", "/tmp",
		"--size", "67108864", "--perms", "0700", "--tmpfs", "/home/agent",
		"--size", "16777216", "--perms", "0700", "--tmpfs", "/run/agent",
	)
	for _, mount := range c.Mounts {
		flag := "--ro-bind"
		if mount.Access == "read-write" {
			flag = "--bind"
		}
		args = append(args, flag, mount.Source, mountPath(c, mount.Name))
	}
	args = append(args, "--ro-bind", worker, "/nx/worker")
	cwd := "/"
	if command.Cwd != "" {
		cwd, err = resolveCwd(c, command.Cwd)
		if err != nil {
			return nil, nil, err
		}
	}
	args = append(args, "--setenv", "PATH", "/usr/local/bin:/usr/bin:/bin", "--setenv", "HOME", "/home/agent",
		"--setenv", "TMPDIR", "/tmp", "--setenv", "XDG_RUNTIME_DIR", "/run/agent", "--setenv", "LANG", "C.UTF-8",
		"--setenv", "WORKSPACE", mountPath(c, "workspace"), "--setenv", "NXCONTAINER_AGENT_ID", c.AgentID)
	keys := make([]string, 0, len(command.Env))
	for key := range command.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--setenv", key, command.Env[key])
	}
	filter, err := seccompFile(network)
	if err != nil {
		return nil, nil, err
	}
	extra := []*os.File{filter}
	argv := command.Argv
	var control *net.UnixConn
	if network {
		var relay *os.File
		control, relay, err = relayControlPair()
		if err != nil {
			filter.Close()
			return nil, nil, err
		}
		// fd 3 为 seccomp 过滤器，fd 4 为中继的控制套接字（见 relayControlFD）。
		extra = append(extra, relay)
		argv = append([]string{"/nx/worker", "--net-relay"}, argv...)
	}
	args = append(args, "--seccomp", "3", "--remount-ro", "/", "--chdir", cwd, "--")
	args = append(args, argv...)
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.ExtraFiles = extra
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return cmd, control, nil
}

func closeExtraFiles(cmd *exec.Cmd) {
	for _, file := range cmd.ExtraFiles {
		_ = file.Close()
	}
}

func seccompFile(network bool) (*os.File, error) {
	program, err := seccompProgram(runtime.GOARCH, network)
	if err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	_, err = write.Write(program)
	_ = write.Close()
	if err != nil {
		_ = read.Close()
		return nil, err
	}
	return read, nil
}
