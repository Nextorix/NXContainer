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
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	config := Config{AgentID: "probe"}
	cmd, err := buildCommand(ctx, config, Command{Argv: []string{"/usr/bin/true"}})
	if err == nil {
		defer closeExtraFiles(cmd)
		var output bytes.Buffer
		cmd.Stderr = &output
		err = cmd.Run()
		if err != nil {
			err = fmt.Errorf("隔离自检失败：%s", strings.TrimSpace(output.String()))
		}
	}
	if err != nil {
		c.Reason = err.Error()
		return c
	}
	c.Exec, c.Files, c.ReadOnlyMounts, c.PrivateFilesystem, c.NetworkNone = true, true, true, true, true
	return c
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
	cmd, err := buildCommand(ctx, c, command)
	if err != nil {
		return -1, err
	}
	defer closeExtraFiles(cmd)
	cmd.Stdin = strings.NewReader(command.Stdin)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func buildCommand(ctx context.Context, c Config, command Command) (*exec.Cmd, error) {
	worker, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := validateMountSources(c, worker); err != nil {
		return nil, err
	}
	bwrap, err := findBubblewrap(ctx)
	if err != nil {
		return nil, err
	}
	system, err := systemArguments()
	if err != nil {
		return nil, err
	}
	args := []string{
		"--unshare-user", "--unshare-pid", "--unshare-net", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup",
		"--disable-userns", "--assert-userns-disabled", "--cap-drop", "ALL", "--new-session", "--die-with-parent", "--clearenv",
	}
	args = append(args, system...)
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
			return nil, err
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
	filter, err := seccompFile()
	if err != nil {
		return nil, err
	}
	args = append(args, "--seccomp", "3", "--remount-ro", "/", "--chdir", cwd, "--")
	args = append(args, command.Argv...)
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.ExtraFiles = []*os.File{filter}
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
	return cmd, nil
}

func closeExtraFiles(cmd *exec.Cmd) {
	for _, file := range cmd.ExtraFiles {
		_ = file.Close()
	}
}

func seccompFile() (*os.File, error) {
	program, err := seccompProgram(runtime.GOARCH)
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
