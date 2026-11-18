//go:build linux

/*
 * NXContainer Linux 后端单元测试
 *
 * 校验受保护文件系统的类型判断，不需要挂载文件系统。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package sandbox

import "testing"

func TestProtectedLinuxFilesystem(t *testing.T) {
	for _, value := range []int64{0x9fa0, 0xcafe4a11, 0xcafe4a11 - 1<<32} {
		if !protectedLinuxFilesystem(value) {
			t.Errorf("未识别受保护文件系统：%#x", value)
		}
	}
	if protectedLinuxFilesystem(0xef53) {
		t.Error("普通 ext 文件系统不应被当成内核接口文件系统")
	}
}
