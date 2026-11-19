//go:build windows

/*
 * NXContainer Windows Backend
 *
 * 将临时目录授权、LPAC 进程、私有桌面与资源配额接入统一执行协议。
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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func windowsEnvironment(home, workspace, agent string, custom map[string]string) (map[string]string, error) {
	root, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return nil, err
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	env := map[string]string{
		"SYSTEMROOT": root, "WINDIR": root, "SYSTEMDRIVE": filepath.VolumeName(root),
		"COMSPEC": filepath.Join(system, "cmd.exe"), "PATH": system,
		"HOME": home, "USERPROFILE": home, "LOCALAPPDATA": home, "APPDATA": home,
		"TEMP": home, "TMP": home, "TMPDIR": home, "WORKSPACE": workspace,
		"NXCONTAINER_AGENT_ID": agent, "PYTHONUTF8": "1",
	}
	for key, value := range custom {
		env[strings.ToUpper(key)] = value
	}
	return env, nil
}

func Probe(ctx context.Context) (capabilities Capabilities) {
	capabilities = Capabilities{Platform: "windows", Architecture: runtime.GOARCH, Backend: "windows-lpac", Status: "experimental", Validation: validationStatus(runtime.GOOS, runtime.GOARCH)}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		capabilities.Reason = "当前只支持 Windows amd64/arm64"
		return capabilities
	}
	cleanupFailed := func(err error) {
		if err == nil {
			return
		}
		capabilities.Exec, capabilities.Files, capabilities.ReadOnlyMounts, capabilities.NetworkNone, capabilities.NetworkInternet = false, false, false, false, false
		capabilities.ProcessQuotas, capabilities.DesktopIsolation = false, false
		capabilities.Reason = "隔离自检清理失败：" + err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	home, err := os.MkdirTemp("", "nxcontainer-probe-")
	if err != nil {
		capabilities.Reason = err.Error()
		return capabilities
	}
	defer func() { cleanupFailed(os.RemoveAll(home)) }()
	profile, err := newWindowsProfile(false)
	if err != nil {
		capabilities.Reason = err.Error()
		return capabilities
	}
	defer func() { cleanupFailed(profile.close()) }()
	desktop, err := newWindowsDesktop(profile)
	if err != nil {
		capabilities.Reason = err.Error()
		return capabilities
	}
	defer desktop.close()
	env, err := windowsEnvironment(home, home, "probe", nil)
	if err != nil {
		capabilities.Reason = err.Error()
		return capabilities
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		capabilities.Reason = err.Error()
		return capabilities
	}
	var stdout, stderr bytes.Buffer
	exit, err := runWindowsProcess(ctx, profile, desktop, windowsProcessOptions{
		Argv: []string{filepath.Join(system, "cmd.exe"), "/d", "/c", "echo", "nxcontainer-lpac"}, Directory: system, Environment: env,
		Stdout: &stdout, Stderr: &stderr, MemoryBytes: 512 * 1024 * 1024, Processes: 32, CPUPercent: 50,
	})
	if err != nil || exit != 0 || !strings.Contains(stdout.String(), "nxcontainer-lpac") {
		capabilities.Reason = fmt.Sprintf("Windows 隔离自检失败：%v，退出码 %d，%s", err, exit, stderr.String())
		return capabilities
	}
	capabilities.Exec, capabilities.Files, capabilities.ReadOnlyMounts, capabilities.NetworkNone = true, true, true, true
	capabilities.ProcessQuotas, capabilities.DesktopIsolation = true, true
	// 联网沙箱只多 internetClient 与 lpacIdentityServices 两个能力；都能派生出能力 SID 即视为支持。
	_, internetErr := windowsCapability("internetClient")
	_, identityErr := windowsCapability("lpacIdentityServices")
	capabilities.NetworkInternet = internetErr == nil && identityErr == nil
	return capabilities
}

func validatePlatform(c Config) error {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return problem("BACKEND_UNAVAILABLE", "当前只支持 Windows amd64/arm64")
	}
	worker, err := os.Executable()
	if err != nil {
		return err
	}
	system, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return err
	}
	for _, mount := range c.Mounts {
		if strings.HasPrefix(mount.Source, `\\`) || len(filepath.VolumeName(mount.Source)) != 2 {
			return problem("UNSAFE_PATH", "Windows 只接受本地盘符目录，不接受 UNC 或设备路径")
		}
		if mount.Access == "read-write" {
			for _, protected := range []string{system, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), worker} {
				if protected != "" && (contained(protected, mount.Source) || contained(mount.Source, protected)) {
					return problem("PERMISSION_DENIED", "不能授予系统、程序安装目录或执行器写权限")
				}
			}
		}
	}
	return nil
}

func copyWindowsWorker(directory string) (string, error) {
	source, err := os.Executable()
	if err != nil {
		return "", err
	}
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	target := filepath.Join(directory, "nxcontainer.exe")
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return target, errors.Join(copyErr, closeErr)
}

func windowsExecutable(c Config, argv []string, cwd string, env map[string]string) (string, error) {
	program := argv[0]
	system, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return "", err
	}
	allowed := func(candidate string) bool {
		if contained(system, candidate) {
			return true
		}
		for _, mount := range c.Mounts {
			if contained(mount.Source, candidate) || (mount.Alias != "" && contained(mount.Alias, candidate)) {
				return true
			}
		}
		return false
	}
	var candidates []string
	if filepath.IsAbs(program) {
		candidates = []string{filepath.Clean(program)}
	} else if strings.ContainsAny(program, `/\`) {
		candidates = []string{filepath.Join(cwd, program)}
	} else {
		for _, directory := range filepath.SplitList(env["PATH"]) {
			if filepath.IsAbs(directory) {
				candidates = append(candidates, filepath.Join(directory, program))
			}
		}
	}
	for _, candidate := range candidates {
		if filepath.Ext(candidate) == "" {
			candidate += ".exe"
		}
		if !allowed(candidate) {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", problem("PERMISSION_DENIED", "找不到授权范围内的可执行文件；请使用系统程序或工作区内的 portable 程序")
}

func runPlatform(ctx context.Context, c Config, command Command, stdout, stderr io.Writer) (exit int, err error) {
	if err := validatePlatform(c); err != nil {
		return -1, err
	}
	// Windows mutex 的所有权属于线程；创建、等待与释放必须留在同一个 OS 线程。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	release, err := lockWindowsSources(ctx, c.Mounts)
	if err != nil {
		return -1, err
	}
	defer release()
	state, err := os.MkdirTemp("", "nxcontainer-run-")
	if err != nil {
		return -1, err
	}
	defer func() {
		if cleanup := os.RemoveAll(state); cleanup != nil {
			err = problem("CLEANUP_FAILED", errors.Join(err, cleanup).Error())
		}
	}()
	home, code := filepath.Join(state, "home"), filepath.Join(state, "code")
	for _, directory := range []string{home, code} {
		if err := os.Mkdir(directory, 0700); err != nil {
			return -1, err
		}
	}
	worker, err := copyWindowsWorker(code)
	if err != nil {
		return -1, err
	}
	profile, err := newWindowsProfile(c.Network == "internet")
	if err != nil {
		return -1, err
	}
	defer func() {
		if cleanup := profile.close(); cleanup != nil {
			err = problem("CLEANUP_FAILED", errors.Join(err, cleanup).Error())
		}
	}()
	desktop, err := newWindowsDesktop(profile)
	if err != nil {
		return -1, err
	}
	defer desktop.close()
	var grants []*windowsGrant
	defer func() {
		for i := len(grants) - 1; i >= 0; i-- {
			if cleanup := grants[i].close(); cleanup != nil {
				err = problem("CLEANUP_FAILED", errors.Join(err, cleanup).Error())
			}
		}
	}()
	mounts := append([]Mount(nil), c.Mounts...)
	mounts = append(mounts, Mount{Source: home, Access: "read-write"}, Mount{Source: code, Access: "read-only"})
	for _, mount := range mounts {
		grant, err := grantWindowsTree(ctx, mount.Source, profile.sid, mount.Access == "read-write")
		if err != nil {
			return -1, err
		}
		grants = append(grants, grant)
		if mount.Alias != "" {
			aliasGrant, err := grantWindowsAlias(mount.Alias, profile.sid)
			if err != nil {
				return -1, err
			}
			grants = append(grants, aliasGrant)
		}
	}
	cwd, err := resolveCwd(c, command.Cwd)
	if err != nil {
		return -1, err
	}
	env, err := windowsEnvironment(home, mountPath(c, "workspace"), c.AgentID, command.Env)
	if err != nil {
		return -1, err
	}
	argv := append([]string(nil), command.Argv...)
	if argv[0] == "/nx/worker" {
		argv[0] = worker
	} else {
		argv[0], err = windowsExecutable(c, argv, cwd, env)
		if err != nil {
			return -1, err
		}
	}
	memory, processes, cpu := c.Limits.MemoryBytes, c.Limits.MaxProcesses, c.Limits.CPUPercent
	if memory == 0 {
		memory = 512 * 1024 * 1024
	}
	if processes == 0 {
		processes = 32
	}
	if cpu == 0 {
		cpu = 50
	}
	return runWindowsProcess(ctx, profile, desktop, windowsProcessOptions{Argv: argv, Directory: cwd, Environment: env, Stdin: command.Stdin, Stdout: stdout, Stderr: stderr, MemoryBytes: memory, Processes: processes, CPUPercent: cpu})
}
