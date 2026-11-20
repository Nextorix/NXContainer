//go:build windows

/*
 * NXContainer Windows 显示联接的最小只读授权。仅打开联接对象自身，不递归、不跟随联接修改目标 ACL。
 * Author: MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package sandbox

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func grantWindowsAlias(alias string, sid *windows.SID) (*windowsGrant, error) {
	grant := &windowsGrant{sid: sid}
	complete := false
	defer func() {
		if !complete {
			_ = grant.close()
		}
	}()
	targets := []string{alias}
	// 卷根已有系统提供的路径属性访问语义，不修改磁盘根目录的 DACL。
	for parent := filepath.Dir(alias); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		targets = append(targets, parent)
	}
	for _, target := range targets {
		name, err := windows.UTF16PtrFromString(target)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			return nil, err
		}
		sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			windows.CloseHandle(handle)
			return nil, err
		}
		grant.backups = append(grant.backups, windowsACLBackup{handle, sd})
		acl, _, err := sd.DACL()
		if err != nil || acl == nil {
			return nil, problem("UNSAFE_PATH", "显示联接及父目录必须有 DACL")
		}
		maskAccess := windows.ACCESS_MASK(windows.FILE_READ_ATTRIBUTES | windows.FILE_TRAVERSE | windows.SYNCHRONIZE)
		if target == alias {
			maskAccess = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE
		}
		updated, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
			// PowerShell 逐级查询路径属性；祖先仅元信息读取和穿越，联接自身可读但不可写。
			AccessPermissions: maskAccess,
			AccessMode:        windows.GRANT_ACCESS, Inheritance: 0,
			Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN, TrusteeValue: windows.TrusteeValueFromSID(sid)},
		}}, acl)
		if err != nil {
			return nil, err
		}
		descriptor, err := windows.NewSecurityDescriptor()
		if err != nil {
			return nil, err
		}
		if err = descriptor.SetDACL(updated, true, false); err != nil {
			return nil, err
		}
		control, _, err := sd.Control()
		if err != nil {
			return nil, err
		}
		mask := windows.SECURITY_DESCRIPTOR_CONTROL(windows.SE_DACL_PROTECTED | windows.SE_DACL_AUTO_INHERITED | windows.SE_DACL_AUTO_INHERIT_REQ)
		if err = descriptor.SetControl(mask, control&mask); err != nil {
			return nil, err
		}
		if err = setWindowsDACL(handle, descriptor); err != nil {
			return nil, err
		}
	}
	complete = true
	return grant, nil
}
