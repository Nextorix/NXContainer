//go:build windows

/*
 * NXContainer 显示联接权限恢复验证。
 * Author: MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */
package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestAliasACLRestoration(t *testing.T) {
	root := t.TempDir()
	source, parent := filepath.Join(root, "source"), filepath.Join(root, "display")
	for _, name := range []string{source, parent} {
		if err := os.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(parent, "workspace")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	profile, err := newWindowsProfile(false)
	if err != nil {
		t.Fatal(err)
	}
	defer profile.close()
	grant, err := grantWindowsAlias(alias, profile.sid)
	if err != nil {
		t.Fatal(err)
	}
	// 对每一个稳定句柄记录原始描述符，恢复后逐一比较，不以文件读写成功代替 ACL 恢复验收。
	type record struct {
		handle windows.Handle
		before string
	}
	var snapshots []record
	for _, backup := range grant.backups {
		var copy windows.Handle
		if err := windows.DuplicateHandle(windows.CurrentProcess(), backup.handle, windows.CurrentProcess(), &copy, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, record{copy, backup.descriptor.String()})
		defer windows.CloseHandle(copy)
	}
	if err := grant.close(); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range snapshots {
		after, err := windows.GetSecurityInfo(snapshot.handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		if after.String() != snapshot.before {
			t.Fatalf("显示联接或祖先 DACL 未恢复：\n原始 %s\n当前 %s", snapshot.before, after.String())
		}
	}
}
