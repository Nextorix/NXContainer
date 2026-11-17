/*
 * NXContainer Native Protocol Tests
 *
 * 验证请求版本、未知字段、大小限制和尾随内容拒绝。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package main

import (
	"bufio"
	"strings"
	"testing"
)

func TestRequest(t *testing.T) {
	valid := `{"version":1,"operation":"capabilities"}` + "\n"
	if _, err := readRequest(bufio.NewReader(strings.NewReader(valid))); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{"version":2,"operation":"capabilities"}`,
		`{"version":1,"operation":"exec","elevated":true}`,
		`{"version":1,"operation":"capabilities"} {}`,
		`{"version":1,"sandbox":{"limits":{"memoryMb":100}}}`,
		strings.Repeat("x", 2*1024*1024+1),
	} {
		if _, err := readRequest(bufio.NewReader(strings.NewReader(invalid + "\n"))); err == nil {
			t.Fatal("无效请求被接受")
		}
	}
}
