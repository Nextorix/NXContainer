//go:build windows

/*
 * NXContainer Windows Process Isolation
 *
 * 创建独立 LPAC 身份、私有桌面与受 Job Object 约束的原生进程。
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
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var winUserenv = windows.NewLazySystemDLL("userenv.dll")
var winCreateProfile = winUserenv.NewProc("CreateAppContainerProfile")
var winDeleteProfile = winUserenv.NewProc("DeleteAppContainerProfile")
var winFreeSID = windows.NewLazySystemDLL("advapi32.dll").NewProc("FreeSid")
var winAccessCheck = windows.NewLazySystemDLL("advapi32.dll").NewProc("AccessCheck")
var winDeriveCapabilities = windows.NewLazySystemDLL("kernelbase.dll").NewProc("DeriveCapabilitySidsFromName")
var winUser32 = windows.NewLazySystemDLL("user32.dll")
var winCreateStation = winUser32.NewProc("CreateWindowStationW")
var winGetStation = winUser32.NewProc("GetProcessWindowStation")
var winSetStation = winUser32.NewProc("SetProcessWindowStation")
var winCloseStation = winUser32.NewProc("CloseWindowStation")
var winCreateDesktop = winUser32.NewProc("CreateDesktopW")
var winCloseDesktop = winUser32.NewProc("CloseDesktop")
var winDesktopMu sync.Mutex

type windowsProfile struct {
	name         string
	sid          *windows.SID
	capabilities []windows.SIDAndAttributes
}

func windowsRegistryReadCapability() ([]windows.SIDAndAttributes, error) {
	if err := winDeriveCapabilities.Find(); err != nil {
		return nil, err
	}
	name, _ := windows.UTF16PtrFromString("registryRead")
	var groups, capabilities **windows.SID
	var groupCount, capabilityCount uint32
	ok, _, callErr := winDeriveCapabilities.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&groups)), uintptr(unsafe.Pointer(&groupCount)), uintptr(unsafe.Pointer(&capabilities)), uintptr(unsafe.Pointer(&capabilityCount)))
	if ok == 0 {
		return nil, callErr
	}
	defer func() {
		for _, list := range []struct {
			pointer **windows.SID
			count   uint32
		}{{groups, groupCount}, {capabilities, capabilityCount}} {
			for _, sid := range unsafe.Slice(list.pointer, list.count) {
				_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(sid)))
			}
			_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(list.pointer)))
		}
	}()
	if capabilityCount != 1 {
		return nil, fmt.Errorf("registryRead 能力 SID 数量异常")
	}
	sid, err := (*capabilities).Copy()
	if err != nil {
		return nil, err
	}
	return []windows.SIDAndAttributes{{Sid: sid, Attributes: windows.SE_GROUP_ENABLED}}, nil
}

// Win32 的 HRESULT 固定为 32 位，Proc.Call 则以指针宽度返回寄存器值。
func profileStatusError(operation string, status uintptr) error {
	if status&0x80000000 != 0 {
		return fmt.Errorf("%s失败：HRESULT 0x%08x", operation, status&0xffffffff)
	}
	return nil
}

func newWindowsProfile() (*windowsProfile, error) {
	if err := winCreateProfile.Find(); err != nil {
		return nil, err
	}
	capabilities, err := windowsRegistryReadCapability()
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	name := "nxcontainer." + hex.EncodeToString(nonce[:])
	value, _ := windows.UTF16PtrFromString(name)
	var sid *windows.SID
	status, _, _ := winCreateProfile.Call(uintptr(unsafe.Pointer(value)), uintptr(unsafe.Pointer(value)), uintptr(unsafe.Pointer(value)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if err := profileStatusError("创建 AppContainer 身份", status); err != nil {
		return nil, err
	}
	return &windowsProfile{name: name, sid: sid, capabilities: capabilities}, nil
}

func (p *windowsProfile) close() error {
	if p.sid == nil {
		return nil
	}
	name, _ := windows.UTF16PtrFromString(p.name)
	status, _, _ := winDeleteProfile.Call(uintptr(unsafe.Pointer(name)))
	winFreeSID.Call(uintptr(unsafe.Pointer(p.sid)))
	p.sid = nil
	return profileStatusError("删除 AppContainer 身份", status)
}

type windowsDesktop struct {
	station, desktop windows.Handle
	name             string
}

func newWindowsDesktop(profile *windowsProfile) (*windowsDesktop, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")(A;;GA;;;" + profile.sid.String() + ")S:(ML;;NW;;;LW)")
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	stationName, _ := windows.UTF16PtrFromString(profile.name)
	desktopName, _ := windows.UTF16PtrFromString("default")
	winDesktopMu.Lock()
	defer winDesktopMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	station, _, callErr := winCreateStation.Call(uintptr(unsafe.Pointer(stationName)), 0, 0x000f037f, uintptr(unsafe.Pointer(&sa)))
	if station == 0 {
		return nil, fmt.Errorf("创建私有 Window Station：%w", callErr)
	}
	original, _, _ := winGetStation.Call()
	ok, _, callErr := winSetStation.Call(station)
	if ok == 0 {
		winCloseStation.Call(station)
		return nil, fmt.Errorf("切换私有 Window Station：%w", callErr)
	}
	desktop, _, createErr := winCreateDesktop.Call(uintptr(unsafe.Pointer(desktopName)), 0, 0, 0, windows.GENERIC_ALL, uintptr(unsafe.Pointer(&sa)))
	ok, _, restoreErr := winSetStation.Call(original)
	if desktop == 0 || ok == 0 {
		if desktop != 0 {
			winCloseDesktop.Call(desktop)
		}
		winCloseStation.Call(station)
		return nil, fmt.Errorf("创建或恢复私有桌面：%v / %v", createErr, restoreErr)
	}
	return &windowsDesktop{windows.Handle(station), windows.Handle(desktop), profile.name + "\\default"}, nil
}

func (d *windowsDesktop) close() {
	winCloseDesktop.Call(uintptr(d.desktop))
	winCloseStation.Call(uintptr(d.station))
}

type windowsProcessOptions struct {
	Argv           []string
	Directory      string
	Environment    map[string]string
	Stdin          string
	Stdout, Stderr io.Writer
	MemoryBytes    uint64
	Processes      uint32
	CPUPercent     uint32
}

type windowsSecurityCapabilities struct {
	AppContainerSID           *windows.SID
	Capabilities              *windows.SIDAndAttributes
	CapabilityCount, Reserved uint32
}

func tokenUint(token windows.Token, class uint32) (uint32, error) {
	var value, length uint32
	err := windows.GetTokenInformation(token, class, (*byte)(unsafe.Pointer(&value)), 4, &length)
	return value, err
}

func tokenBuffer(token windows.Token, class uint32) ([]byte, error) {
	var length uint32
	err := windows.GetTokenInformation(token, class, nil, 0, &length)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || length == 0 || length > 1024*1024 {
		return nil, fmt.Errorf("查询令牌信息大小：%v", err)
	}
	buffer := make([]byte, length)
	if err := windows.GetTokenInformation(token, class, &buffer[0], length, &length); err != nil {
		return nil, err
	}
	return buffer, nil
}

// 低盒令牌可能仍保留可重新启用的宿主特权；在主线程恢复前永久删除，而不是仅禁用。
func restrictWindowsPrivileges(process windows.Handle) error {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY|windows.TOKEN_ADJUST_PRIVILEGES, &token); err != nil {
		return err
	}
	defer token.Close()
	buffer, err := tokenBuffer(token, windows.TokenPrivileges)
	if err != nil {
		return err
	}
	name, _ := windows.UTF16PtrFromString("SeChangeNotifyPrivilege")
	var traversal windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &traversal); err != nil {
		return err
	}
	privileges := (*windows.Tokenprivileges)(unsafe.Pointer(&buffer[0]))
	for _, privilege := range privileges.AllPrivileges() {
		if privilege.Luid == traversal {
			continue
		}
		update := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: privilege.Luid, Attributes: windows.SE_PRIVILEGE_REMOVED}}}
		if err := windows.AdjustTokenPrivileges(token, false, &update, 0, nil, nil); err != nil {
			return err
		}
	}
	runtime.KeepAlive(buffer)
	return nil
}

func verifyWindowsIdentity(process windows.Handle, profile *windowsProfile) error {
	sid := profile.sid
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		return err
	}
	defer token.Close()
	value, err := tokenUint(token, 29)
	if err != nil || value != 1 {
		return fmt.Errorf("子进程不是 AppContainer：%v", err)
	}
	buffer, err := tokenBuffer(token, 31)
	if err != nil {
		return err
	}
	actual := *(**windows.SID)(unsafe.Pointer(&buffer[0]))
	if actual == nil || !windows.EqualSid(actual, sid) {
		return fmt.Errorf("子进程 AppContainer SID 不匹配")
	}
	runtime.KeepAlive(buffer)
	capabilityBuffer, err := tokenBuffer(token, 30)
	if err != nil {
		return err
	}
	groups := (*windows.Tokengroups)(unsafe.Pointer(&capabilityBuffer[0]))
	if int(groups.GroupCount) != len(profile.capabilities) {
		return fmt.Errorf("子进程能力数量不匹配")
	}
	for index, capability := range groups.AllGroups() {
		if !windows.EqualSid(capability.Sid, profile.capabilities[index].Sid) {
			return fmt.Errorf("子进程具有非预期能力")
		}
	}
	runtime.KeepAlive(capabilityBuffer)
	privilegeBuffer, err := tokenBuffer(token, windows.TokenPrivileges)
	if err != nil {
		return err
	}
	name, _ := windows.UTF16PtrFromString("SeChangeNotifyPrivilege")
	var traversal windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &traversal); err != nil {
		return err
	}
	tokenPrivileges := (*windows.Tokenprivileges)(unsafe.Pointer(&privilegeBuffer[0]))
	for _, privilege := range tokenPrivileges.AllPrivileges() {
		if privilege.Luid != traversal {
			return fmt.Errorf("LPAC 令牌保留了非预期特权")
		}
	}
	runtime.KeepAlive(privilegeBuffer)
	labelBuffer, err := tokenBuffer(token, windows.TokenIntegrityLevel)
	if err != nil {
		return err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&labelBuffer[0]))
	count := label.Label.Sid.SubAuthorityCount()
	if count == 0 || label.Label.Sid.SubAuthority(uint32(count-1)) > 4096 {
		return fmt.Errorf("LPAC 完整性级别高于低完整性")
	}
	runtime.KeepAlive(labelBuffer)
	uiAccess, err := tokenUint(token, windows.TokenUIAccess)
	if err != nil || uiAccess != 0 {
		return fmt.Errorf("LPAC 不能具有 UIAccess：%v", err)
	}
	// 用文档化的 AccessCheck 验证 LPAC：只允许受限应用包的位 2，不得得到普通应用包的位 1。
	// 不依赖部分 Windows 版本尚未支持的 TokenIsLessPrivilegedAppContainer 查询类。
	var impersonation windows.Token
	if err := windows.DuplicateTokenEx(token, windows.TOKEN_QUERY, nil, windows.SecurityIdentification, windows.TokenImpersonation, &impersonation); err != nil {
		return err
	}
	defer impersonation.Close()
	sd, err := windows.SecurityDescriptorFromString("O:SYG:SYD:(A;;0x3;;;WD)(A;;0x1;;;AC)(A;;0x2;;;S-1-15-2-2)")
	if err != nil {
		return err
	}
	var mapping [4]uint32
	var privileges [256]byte
	length, granted, allowed := uint32(len(privileges)), uint32(0), uint32(0)
	ok, _, callErr := winAccessCheck.Call(uintptr(unsafe.Pointer(sd)), uintptr(impersonation), 0x02000000, uintptr(unsafe.Pointer(&mapping[0])), uintptr(unsafe.Pointer(&privileges[0])), uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&granted)), uintptr(unsafe.Pointer(&allowed)))
	if ok == 0 || allowed == 0 || granted != 2 {
		return fmt.Errorf("LPAC 权限自检失败：%v，授权位 0x%x", callErr, granted)
	}
	return nil
}

func createWindowsJob(options windowsProcessOptions) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = options.Processes
	limits.JobMemoryLimit = uintptr(options.MemoryBytes)
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	cpu := struct{ Flags, Rate uint32 }{5, options.CPUPercent * 100}
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	ui := windows.JOBOBJECT_BASIC_UI_RESTRICTIONS{UIRestrictionsClass: 0xff}
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectBasicUIRestrictions, uintptr(unsafe.Pointer(&ui)), uint32(unsafe.Sizeof(ui))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func waitWindowsJobEmpty(job windows.Handle) error {
	var accounting struct {
		TotalUserTime, TotalKernelTime, PeriodUserTime, PeriodKernelTime int64
		PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return err
		}
		if accounting.ActiveProcesses == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return problem("CLEANUP_FAILED", "Job 中仍有未终止的进程")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runWindowsProcess(ctx context.Context, profile *windowsProfile, desktop *windowsDesktop, options windowsProcessOptions) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	job, err := createWindowsJob(options)
	if err != nil {
		return -1, err
	}
	defer windows.CloseHandle(job)
	stdin, input, err := os.Pipe()
	if err != nil {
		return -1, err
	}
	defer stdin.Close()
	defer input.Close()
	output, stdout, err := os.Pipe()
	if err != nil {
		return -1, err
	}
	defer output.Close()
	defer stdout.Close()
	errorsOut, stderr, err := os.Pipe()
	if err != nil {
		return -1, err
	}
	defer errorsOut.Close()
	defer stderr.Close()
	handles := []windows.Handle{windows.Handle(stdin.Fd()), windows.Handle(stdout.Fd()), windows.Handle(stderr.Fd())}
	for _, handle := range handles {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return -1, err
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(4)
	if err != nil {
		return -1, err
	}
	defer attributes.Delete()
	// registryRead 仅用于初始化 Win32 运行库（包括 Winsock 目录）；不授予任何网络能力。
	if len(profile.capabilities) != 1 {
		return -1, fmt.Errorf("Windows 进程必须恰有一个 registryRead 能力")
	}
	capabilities := windowsSecurityCapabilities{AppContainerSID: profile.sid, Capabilities: &profile.capabilities[0], CapabilityCount: 1}
	policy := uint32(1)
	for _, attribute := range []struct {
		key   uintptr
		value unsafe.Pointer
		size  uintptr
	}{
		{0x20009, unsafe.Pointer(&capabilities), unsafe.Sizeof(capabilities)},
		{0x2000f, unsafe.Pointer(&policy), unsafe.Sizeof(policy)},
		{0x2000d, unsafe.Pointer(&job), unsafe.Sizeof(job)},
		{windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles)) * unsafe.Sizeof(handles[0])},
	} {
		if err := attributes.Update(attribute.key, attribute.value, attribute.size); err != nil {
			return -1, fmt.Errorf("设置进程属性 0x%x：%w", attribute.key, err)
		}
	}
	envKeys := make([]string, 0, len(options.Environment))
	for key := range options.Environment {
		envKeys = append(envKeys, key)
	}
	sort.Slice(envKeys, func(i, j int) bool { return strings.ToUpper(envKeys[i]) < strings.ToUpper(envKeys[j]) })
	var env []uint16
	for _, key := range envKeys {
		env = append(env, utf16.Encode([]rune(key+"="+options.Environment[key]))...)
		env = append(env, 0)
	}
	env = append(env, 0)
	if len(env) == 1 {
		env = append(env, 0)
	}
	application, err := windows.UTF16PtrFromString(options.Argv[0])
	if err != nil {
		return -1, err
	}
	commandText := windows.ComposeCommandLine(options.Argv)
	if strings.EqualFold(filepath.Base(options.Argv[0]), "cmd.exe") && len(options.Argv) > 2 {
		index := len(options.Argv) - 2
		if strings.EqualFold(options.Argv[index], "/c") || strings.EqualFold(options.Argv[index], "/k") {
			commandText = windows.ComposeCommandLine(options.Argv[:index+1]) + ` "` + options.Argv[index+1] + `"`
		}
	}
	commandLine, err := windows.UTF16FromString(commandText)
	if err != nil {
		return -1, err
	}
	directory, err := windows.UTF16PtrFromString(options.Directory)
	if err != nil {
		return -1, err
	}
	desktopName, _ := windows.UTF16PtrFromString(desktop.name)
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Desktop = desktopName
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = handles[0], handles[1], handles[2]
	var process windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW)
	if err := windows.CreateProcess(application, &commandLine[0], nil, nil, true, flags, &env[0], directory, &startup.StartupInfo, &process); err != nil {
		return -1, fmt.Errorf("创建 LPAC 进程：%w", err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	defer windows.TerminateJobObject(job, 1)
	if err := restrictWindowsPrivileges(process.Process); err != nil {
		return -1, err
	}
	if err := verifyWindowsIdentity(process.Process, profile); err != nil {
		return -1, err
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		return -1, err
	}
	stdin.Close()
	stdout.Close()
	stderr.Close()
	var streams sync.WaitGroup
	streams.Add(3)
	go func() { defer streams.Done(); _, _ = io.Copy(options.Stdout, output) }()
	go func() { defer streams.Done(); _, _ = io.Copy(options.Stderr, errorsOut) }()
	go func() { defer streams.Done(); _, _ = io.WriteString(input, options.Stdin); _ = input.Close() }()
	var waitErr error
	for {
		status, err := windows.WaitForSingleObject(process.Process, 25)
		if err != nil {
			waitErr = err
			break
		}
		if status == windows.WAIT_OBJECT_0 {
			break
		}
		if ctx.Err() != nil {
			waitErr = ctx.Err()
			break
		}
	}
	// 无论主进程如何退出，都不保留后台子进程；先终止整个 Job，再释放文件授权。
	if err := windows.TerminateJobObject(job, 1); err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		waitErr = errors.Join(waitErr, err)
	}
	_, _ = windows.WaitForSingleObject(process.Process, 5000)
	waitErr = errors.Join(waitErr, waitWindowsJobEmpty(job))
	_ = input.Close()
	done := make(chan struct{})
	go func() { streams.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = output.Close()
		_ = errorsOut.Close()
		<-done
		waitErr = errors.Join(waitErr, fmt.Errorf("等待隔离管道关闭超时"))
	}
	var exit uint32
	if err := windows.GetExitCodeProcess(process.Process, &exit); err != nil {
		return -1, errors.Join(waitErr, err)
	}
	return int(exit), waitErr
}
