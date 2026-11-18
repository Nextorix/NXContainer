/*
 * NXContainer System Call Filter Tests
 *
 * 解释并验证 BPF 过滤规则、体系结构检查和关键拒绝条件。
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
	"encoding/binary"
	"testing"
)

// 解释本项目生成的少量经典 BPF 指令，以覆盖架构混淆和关键拒绝规则。
func evaluateFilter(t *testing.T, program []byte, arch, call uint32) uint32 {
	t.Helper()
	var accumulator uint32
	for pc := 0; pc < len(program)/8; pc++ {
		i := program[pc*8 : pc*8+8]
		code, jt, jf, k := binary.LittleEndian.Uint16(i), int(i[2]), int(i[3]), binary.LittleEndian.Uint32(i[4:])
		switch code {
		case 0x20:
			if k == 4 {
				accumulator = arch
			} else if k == 0 {
				accumulator = call
			} else {
				t.Fatal("未知载入位置")
			}
		case 0x15:
			if accumulator == k {
				pc += jt
			} else {
				pc += jf
			}
		case 0x45:
			if accumulator&k != 0 {
				pc += jt
			} else {
				pc += jf
			}
		case 0x06:
			return k
		default:
			t.Fatal("未知 BPF 指令")
		}
	}
	t.Fatal("过滤器没有终止")
	return 0
}

func TestSeccomp(t *testing.T) {
	for _, tc := range []struct {
		arch                 string
		audit, socket, write uint32
	}{{"amd64", 0xc000003e, 41, 1}, {"arm64", 0xc00000b7, 198, 64}} {
		program, err := seccompProgram(tc.arch)
		if err != nil {
			t.Fatal(err)
		}
		if evaluateFilter(t, program, tc.audit, tc.socket) != 0x50001 {
			t.Fatal("没有拒绝 socket")
		}
		if evaluateFilter(t, program, tc.audit, 425) != 0x50001 {
			t.Fatal("没有拒绝 io_uring")
		}
		if evaluateFilter(t, program, tc.audit, 435) != 0x50026 {
			t.Fatal("clone3 没有返回 ENOSYS")
		}
		if evaluateFilter(t, program, tc.audit, tc.write) != 0x7fff0000 {
			t.Fatal("错误拒绝普通写入")
		}
		if evaluateFilter(t, program, 0x40000003, tc.write) != 0x80000000 {
			t.Fatal("没有阻止架构绕过")
		}
		if tc.arch == "amd64" && evaluateFilter(t, program, tc.audit, 0x40000001) != 0x80000000 {
			t.Fatal("没有阻止 x32 ABI")
		}
	}
	if _, err := seccompProgram("386"); err == nil {
		t.Fatal("不应支持未经验证的架构")
	}
}
