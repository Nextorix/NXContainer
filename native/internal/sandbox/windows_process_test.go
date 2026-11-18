//go:build windows

/*
 * NXContainer Windows Process Tests
 *
 * 在真实 Windows 上验证 LPAC 身份、私有桌面与 Job Object 执行。
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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestProfileStatusError(t *testing.T) {
	for _, status := range []uintptr{0, 1, 0x7fffffff} {
		if err := profileStatusError("测试", status); err != nil {
			t.Fatal(err)
		}
	}
	if err := profileStatusError("测试", 0x80070005); err == nil || !strings.Contains(err.Error(), "0x80070005") {
		t.Fatalf("应保留 HRESULT 的 32 位错误码：%v", err)
	}
}

func TestWindowsJobSettings(t *testing.T) {
	job, err := createWindowsJob(windowsProcessOptions{MemoryBytes: 128 * 1024 * 1024, Processes: 2, CPUPercent: 25})
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		t.Fatal(err)
	}
	if limits.JobMemoryLimit != 128*1024*1024 || limits.BasicLimitInformation.ActiveProcessLimit != 2 || limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatal("Job 资源限制未生效")
	}
	var cpu struct{ Flags, Rate uint32 }
	if err := windows.QueryInformationJobObject(job, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu)), nil); err != nil {
		t.Fatal(err)
	}
	if cpu.Flags != 5 || cpu.Rate != 2500 {
		t.Fatal("CPU 硬限制未生效")
	}
}

func TestWindowsGuestBoundary(t *testing.T) {
	if os.Getenv("NX_TEST_GUEST") != "1" {
		t.Skip("仅在 LPAC 子进程中运行")
	}
	pid, err := strconv.ParseUint(os.Getenv("NX_TEST_PARENT"), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, mask := range []uint32{windows.PROCESS_VM_READ, windows.PROCESS_VM_WRITE, windows.PROCESS_DUP_HANDLE, windows.PROCESS_TERMINATE, windows.PROCESS_CREATE_THREAD} {
		handle, err := windows.OpenProcess(mask, false, uint32(pid))
		if err == nil {
			windows.CloseHandle(handle)
			t.Fatalf("能打开宿主进程的敏感权限：0x%x", mask)
		}
	}
	open := winUser32.NewProc("OpenWindowStationW")
	for _, target := range []string{"WinSta0", os.Getenv("NX_TEST_OTHER_STATION")} {
		name, _ := windows.UTF16PtrFromString(target)
		for _, mask := range []uintptr{0x0200, 0x0004, 0x0001} {
			station, _, _ := open.Call(uintptr(unsafe.Pointer(name)), 0, mask)
			if station != 0 {
				winCloseStation.Call(station)
				t.Fatalf("能打开外部 Window Station：%s，权限 0x%x", target, mask)
			}
		}
	}
	t.Log("NX-BOUNDARY-OK")
}

func TestWindowsProcessAndDesktopBoundary(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	program := filepath.Join(workspace, "boundary.exe")
	output, err := os.Create(program)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	profile, err := newWindowsProfile()
	if err != nil {
		t.Fatal(err)
	}
	defer profile.close()
	desktop, err := newWindowsDesktop(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.close()
	other, err := newWindowsProfile()
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()
	otherDesktop, err := newWindowsDesktop(other)
	if err != nil {
		t.Fatal(err)
	}
	defer otherDesktop.close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	grant, err := grantWindowsTree(ctx, workspace, profile.sid, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := grant.close(); err != nil {
			t.Error(err)
		}
	}()
	env, err := windowsEnvironment(workspace, workspace, "boundary", map[string]string{"NX_TEST_GUEST": "1", "NX_TEST_PARENT": strconv.Itoa(os.Getpid()), "NX_TEST_OTHER_STATION": other.name})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit, err := runWindowsProcess(ctx, profile, desktop, windowsProcessOptions{Argv: []string{program, "-test.run=^TestWindowsGuestBoundary$", "-test.v"}, Directory: workspace, Environment: env, Stdout: &stdout, Stderr: &stderr, MemoryBytes: 512 * 1024 * 1024, Processes: 16, CPUPercent: 50})
	if err != nil || exit != 0 || !strings.Contains(stdout.String(), "NX-BOUNDARY-OK") {
		t.Fatalf("边界测试失败：%d / %v / %s / %s", exit, err, stdout.String(), stderr.String())
	}
}

func TestWindowsLPACProcess(t *testing.T) {
	profile, err := newWindowsProfile()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := profile.close(); err != nil {
			t.Error(err)
		}
	}()
	desktop, err := newWindowsDesktop(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.close()
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	windowsDir, err := windows.GetWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	exit, err := runWindowsProcess(ctx, profile, desktop, windowsProcessOptions{
		Argv:      []string{filepath.Join(system, "cmd.exe"), "/d", "/c", "echo", "nx-lpac-ok"},
		Directory: system,
		Environment: map[string]string{"SystemRoot": windowsDir, "WINDIR": windowsDir, "PATH": system,
			"USERPROFILE": os.Getenv("USERPROFILE"), "LOCALAPPDATA": os.Getenv("LOCALAPPDATA"), "APPDATA": os.Getenv("APPDATA"),
			"TEMP": os.TempDir(), "TMP": os.TempDir(), "SystemDrive": filepath.VolumeName(system)},
		Stdout: &stdout, Stderr: &stderr, MemoryBytes: 512 * 1024 * 1024, Processes: 16, CPUPercent: 50,
	})
	if err != nil || exit != 0 || !strings.Contains(stdout.String(), "nx-lpac-ok") {
		t.Fatalf("LPAC 执行失败：退出码 %d，错误 %v，输出 %q，错误流 %q", exit, err, stdout.String(), stderr.String())
	}
}

func TestWindowsLPACFileGrants(t *testing.T) {
	workspace, peer, outside, home := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for _, directory := range []string{workspace, peer, outside} {
		if err := os.WriteFile(filepath.Join(directory, "existing.txt"), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	original, err := windows.GetNamedSecurityInfo(filepath.Join(peer, "existing.txt"), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := newWindowsProfile()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := profile.close(); err != nil {
			t.Error(err)
		}
	}()
	desktop, err := newWindowsDesktop(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var grants []*windowsGrant
	defer func() {
		for _, grant := range grants {
			if err := grant.close(); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, source := range []struct {
		path     string
		writable bool
	}{{workspace, true}, {peer, false}, {home, true}} {
		grant, err := grantWindowsTree(ctx, source.path, profile.sid, source.writable)
		if err != nil {
			t.Fatal(err)
		}
		grants = append(grants, grant)
	}
	system, _ := windows.GetSystemDirectory()
	windowsDir, _ := windows.GetWindowsDirectory()
	env := map[string]string{"SystemRoot": windowsDir, "WINDIR": windowsDir, "PATH": system, "SystemDrive": filepath.VolumeName(system),
		"USERPROFILE": home, "LOCALAPPDATA": home, "APPDATA": home, "TEMP": home, "TMP": home,
		"WORKSPACE": workspace, "PEER": peer, "OUTSIDE": outside}
	for _, tc := range []struct {
		name, command string
		success       bool
	}{
		{"写自己的旧文件", `echo changed>"%WORKSPACE%\existing.txt"`, true},
		{"创建自己的文件", `echo new>"%WORKSPACE%\new.txt"`, true},
		{"读取只读共享文件", `type "%PEER%\existing.txt"`, true},
		{"不能修改只读共享文件", `echo attack>"%PEER%\existing.txt"`, false},
		{"不能删除只读共享文件", `del "%PEER%\existing.txt" & if exist "%PEER%\existing.txt" (exit /b 1) else (exit /b 0)`, false},
		{"不能读取未授权文件", `type "%OUTSIDE%\existing.txt"`, false},
		{"不能修改只读文件的权限", `icacls "%PEER%\existing.txt" /grant *S-1-1-0:F`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit, err := runWindowsProcess(ctx, profile, desktop, windowsProcessOptions{Argv: []string{filepath.Join(system, "cmd.exe"), "/d", "/s", "/c", tc.command}, Directory: workspace, Environment: env, Stdout: &stdout, Stderr: &stderr, MemoryBytes: 512 * 1024 * 1024, Processes: 16, CPUPercent: 50})
			if err != nil || (exit == 0) != tc.success {
				t.Fatalf("退出码 %d，错误 %v，输出 %q，错误流 %q", exit, err, stdout.String(), stderr.String())
			}
		})
	}
	t.Run("并存身份不能读取另一身份的文件", func(t *testing.T) {
		other, err := newWindowsProfile()
		if err != nil {
			t.Fatal(err)
		}
		defer other.close()
		if windows.EqualSid(profile.sid, other.sid) {
			t.Fatal("AppContainer 身份发生复用")
		}
		otherDesktop, err := newWindowsDesktop(other)
		if err != nil {
			t.Fatal(err)
		}
		defer otherDesktop.close()
		var stdout, stderr bytes.Buffer
		exit, err := runWindowsProcess(ctx, other, otherDesktop, windowsProcessOptions{Argv: []string{filepath.Join(system, "cmd.exe"), "/d", "/s", "/c", `type "%WORKSPACE%\new.txt"`}, Directory: system, Environment: env, Stdout: &stdout, Stderr: &stderr, MemoryBytes: 512 * 1024 * 1024, Processes: 16, CPUPercent: 50})
		if err != nil || exit == 0 {
			t.Fatalf("跨身份读取结果：%d / %v / %s", exit, err, stdout.String())
		}
	})
	data, err := os.ReadFile(filepath.Join(peer, "existing.txt"))
	if err != nil || string(data) != "original" {
		t.Fatal("只读共享文件被改变")
	}
	for _, grant := range grants {
		if err := grant.close(); err != nil {
			t.Fatal(err)
		}
	}
	grants = nil
	for _, target := range []string{workspace, filepath.Join(workspace, "new.txt"), filepath.Join(workspace, "existing.txt")} {
		descriptor, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(descriptor.String(), profile.sid.String()) {
			t.Fatalf("授权 SID 在 %s 中残留", target)
		}
	}
	restored, err := windows.GetNamedSecurityInfo(filepath.Join(peer, "existing.txt"), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || restored.String() != original.String() {
		t.Fatalf("DACL 未恢复：%v；原始 %s；当前 %s", err, original.String(), restored.String())
	}
}
