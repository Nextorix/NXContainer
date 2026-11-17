//go:build windows

/*
 * NXContainer Windows File Grants
 *
 * 通过目录根约束和稳定文件句柄授予临时权限，结束后恢复原始 DACL。
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
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var winSetObjectSecurity = windows.NewLazySystemDLL("advapi32.dll").NewProc("SetKernelObjectSecurity")
var winDeleteACE = windows.NewLazySystemDLL("advapi32.dll").NewProc("DeleteAce")

// 此接口只修改已打开的对象，不递归传播目录 ACE；不能替换成按路径递归改 ACL 的实现。
func setWindowsDACL(handle windows.Handle, descriptor *windows.SECURITY_DESCRIPTOR) error {
	control, _, controlErr := descriptor.Control()
	if controlErr != nil {
		return controlErr
	}
	flags := uintptr(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		flags = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if control&windows.SE_DACL_AUTO_INHERITED != 0 {
		if control&windows.SE_SELF_RELATIVE != 0 {
			var err error
			descriptor, err = descriptor.ToAbsolute()
			if err != nil {
				return err
			}
		}
		if err := descriptor.SetControl(windows.SE_DACL_AUTO_INHERIT_REQ, windows.SE_DACL_AUTO_INHERIT_REQ); err != nil {
			return err
		}
	}
	ok, _, err := winSetObjectSecurity.Call(uintptr(handle), flags, uintptr(unsafe.Pointer(descriptor)))
	if ok == 0 {
		return err
	}
	return nil
}

type windowsACLBackup struct {
	handle     windows.Handle
	descriptor *windows.SECURITY_DESCRIPTOR
}

type windowsGrant struct {
	backups []windowsACLBackup
	root    *os.Root
	base    *os.File
	sid     *windows.SID
}

func openWindowsACL(base *os.File, name string) (windows.Handle, error) {
	relative := strings.ReplaceAll(name, "/", "\\")
	if name == "." {
		relative = ""
	}
	objectName, err := windows.NewNTUnicodeString(relative)
	if err != nil {
		return 0, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(base.Fd()), ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var status windows.IO_STATUS_BLOCK
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES, &attributes, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
	return handle, err
}

func removeWindowsGrant(handle windows.Handle, sid *windows.SID) error {
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	acl, _, err := descriptor.DACL()
	if err != nil || acl == nil {
		return err
	}
	// ACL 头部的 AceCount 位于偏移 4；仅删除本执行器添加的普通允许 ACE，其他内容原样保留。
	count := *(*uint16)(unsafe.Add(unsafe.Pointer(acl), 4))
	changed := false
	for index := int(count) - 1; index >= 0; index-- {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, uint32(index), &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), sid) {
			continue
		}
		ok, _, err := winDeleteACE.Call(uintptr(unsafe.Pointer(acl)), uintptr(index))
		if ok == 0 {
			return err
		}
		changed = true
	}
	if changed {
		return setWindowsDACL(handle, descriptor)
	}
	return nil
}

func (grant *windowsGrant) close() error {
	var result error
	for index := len(grant.backups) - 1; index >= 0; index-- {
		backup := grant.backups[index]
		err := setWindowsDACL(backup.handle, backup.descriptor)
		// 已被任务删除的文件无须恢复；句柄在关闭前仍保持对象身份。
		if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_DELETE_PENDING) {
			result = errors.Join(result, err)
		}
		_ = windows.CloseHandle(backup.handle)
	}
	grant.backups = nil
	if grant.root == nil {
		return result
	}
	// 先恢复旧对象，再清理新建对象继承的租约 SID。始终使用原始目录句柄，不跟随任务创建的 junction。
	count := 0
	err := fs.WalkDir(grant.root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > 65536 {
			return problem("FILE_LIMIT", "Windows 清理目录超过 65536 项")
		}
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return nil
		}
		handle, err := openWindowsACL(grant.base, name)
		if err != nil {
			return err
		}
		defer windows.CloseHandle(handle)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			return err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!entry.IsDir() && info.NumberOfLinks != 1) {
			return nil
		}
		return removeWindowsGrant(handle, grant.sid)
	})
	result = errors.Join(result, err, grant.base.Close(), grant.root.Close())
	grant.base, grant.root = nil, nil
	return result
}

func grantWindowsTree(ctx context.Context, source string, sid *windows.SID, writable bool) (*windowsGrant, error) {
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, err
	}
	base, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	grant := &windowsGrant{root: root, base: base, sid: sid}
	complete := false
	defer func() {
		if !complete {
			_ = grant.close()
		}
	}()
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(grant.backups) >= 65536 {
			return problem("FILE_LIMIT", "Windows 授权目录超过 65536 项")
		}
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return problem("UNSAFE_PATH", "Windows 授权目录不能包含链接或特殊文件")
		}
		handle, err := openWindowsACL(base, name)
		if err != nil {
			return fmt.Errorf("无法取得目录授权所需的 WRITE_DAC：%w", err)
		}
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			windows.CloseHandle(handle)
			return err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!entry.IsDir() && info.NumberOfLinks != 1) {
			windows.CloseHandle(handle)
			return problem("UNSAFE_PATH", "Windows 授权目录不能包含重解析点或硬链接")
		}
		sd, err := windows.GetSecurityInfo(windows.Handle(handle), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			windows.CloseHandle(windows.Handle(handle))
			return err
		}
		grant.backups = append(grant.backups, windowsACLBackup{windows.Handle(handle), sd})
		acl, _, err := sd.DACL()
		if err != nil || acl == nil {
			return problem("UNSAFE_PATH", "不接受缺失 DACL 的授权目录")
		}
		mask := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)
		if writable {
			mask |= windows.FILE_GENERIC_WRITE | windows.DELETE | 0x0040
			if name == "." {
				mask &^= windows.DELETE
			}
		}
		inheritance := uint32(0)
		if entry.IsDir() {
			inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
		}
		updated, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
			AccessPermissions: mask, AccessMode: windows.GRANT_ACCESS, Inheritance: inheritance,
			Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN, TrusteeValue: windows.TrusteeValueFromSID(sid)},
		}}, acl)
		if err != nil {
			return err
		}
		descriptor, err := windows.NewSecurityDescriptor()
		if err != nil {
			return err
		}
		if err := descriptor.SetDACL(updated, true, false); err != nil {
			return err
		}
		control, _, err := sd.Control()
		if err != nil {
			return err
		}
		maskControl := windows.SE_DACL_PROTECTED | windows.SE_DACL_AUTO_INHERITED | windows.SE_DACL_AUTO_INHERIT_REQ
		if err := descriptor.SetControl(windows.SECURITY_DESCRIPTOR_CONTROL(maskControl), control&windows.SECURITY_DESCRIPTOR_CONTROL(maskControl)); err != nil {
			return err
		}
		return setWindowsDACL(handle, descriptor)
	})
	if err != nil {
		return nil, err
	}
	complete = true
	return grant, nil
}

// 必须在锁定的 OS 线程上调用和释放。共享源目录在一次执行期间串行授权，避免恢复 ACL 时覆盖另一个租约。
func lockWindowsSources(ctx context.Context, mounts []Mount) (func(), error) {
	keys := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		keys = append(keys, strings.ToLower(mount.Source))
	}
	sort.Strings(keys)
	var held []windows.Handle
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = windows.ReleaseMutex(held[i])
			_ = windows.CloseHandle(held[i])
		}
		held = nil
	}
	for _, source := range keys {
		name, _ := windows.UTF16PtrFromString(fmt.Sprintf("Global\\NXContainer.Mount.%x", sha256.Sum256([]byte(source))))
		handle, err := windows.CreateMutex(nil, false, name)
		if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			release()
			return nil, err
		}
		for {
			status, err := windows.WaitForSingleObject(handle, 25)
			if err != nil || ctx.Err() != nil {
				if err == nil && (status == windows.WAIT_OBJECT_0 || status == windows.WAIT_ABANDONED) {
					_ = windows.ReleaseMutex(handle)
				}
				windows.CloseHandle(handle)
				release()
				return nil, errors.Join(err, ctx.Err())
			}
			if status == windows.WAIT_OBJECT_0 {
				held = append(held, handle)
				break
			}
			if status == windows.WAIT_ABANDONED {
				_ = windows.ReleaseMutex(handle)
				windows.CloseHandle(handle)
				release()
				return nil, problem("RECOVERY_REQUIRED", "上次目录授权异常退出，需要检查原 ACL 后再执行")
			}
		}
	}
	return release, nil
}
