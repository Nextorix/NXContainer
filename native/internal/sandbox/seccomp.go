/*
 * NXContainer System Call Filter
 *
 * 生成带体系结构检查的 Linux 经典 BPF 系统调用过滤程序。
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
	"encoding/binary"
)

// 经典 BPF：校验体系结构，禁止跨沙箱 IPC、内核管理及绕开同步系统调用过滤的入口。
// 默认断网时也禁止创建 Unix socket，防止只读共享目录中的宿主/其他 Agent socket 被连接。
func seccompProgram(arch string) ([]byte, error) {
	var audit uint32
	var denied []uint32
	switch arch {
	case "amd64":
		audit = 0xc000003e
		denied = []uint32{41, 101, 155, 163, 165, 166, 167, 168, 169, 175, 176, 179, 246, 248, 249, 250, 272, 298, 300, 303, 304, 308, 310, 311, 313, 320, 321, 323, 425}
	case "arm64":
		audit = 0xc00000b7
		denied = []uint32{39, 40, 41, 60, 89, 97, 104, 105, 106, 117, 142, 198, 217, 218, 219, 224, 225, 241, 262, 264, 265, 268, 270, 271, 273, 280, 282, 294, 425}
	default:
		return nil, problem("BACKEND_UNAVAILABLE", "没有此架构的系统调用过滤器")
	}
	type instruction struct {
		Code uint16
		JT   uint8
		JF   uint8
		K    uint32
	}
	program := []instruction{{0x20, 0, 0, 4}, {0x15, 1, 0, audit}, {0x06, 0, 0, 0x80000000}, {0x20, 0, 0, 0}}
	if arch == "amd64" {
		program = append(program, instruction{0x45, 0, 1, 0x40000000}, instruction{0x06, 0, 0, 0x80000000})
	}
	// clone3 的参数在用户内存中，不能直接用经典 BPF 检查；ENOSYS 允许 libc 回退。
	program = append(program, instruction{0x15, 0, 1, 435}, instruction{0x06, 0, 0, 0x00050026})
	for _, number := range denied {
		program = append(program, instruction{0x15, 0, 1, number}, instruction{0x06, 0, 0, 0x00050001})
	}
	program = append(program, instruction{0x06, 0, 0, 0x7fff0000})
	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.LittleEndian, program); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
