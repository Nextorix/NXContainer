//go:build !linux && !windows

/*
 * NXContainer Unsupported Backend Tests
 *
 * 验证未实现的平台始终拒绝执行，不产生宿主命令输出。
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
	"context"
	"errors"
	"testing"
)

func TestUnsupportedNeverExecutes(t *testing.T) {
	config := testConfig(t)
	capabilities := Probe(context.Background())
	if capabilities.Exec || capabilities.Files || capabilities.Desktop {
		t.Fatal("未实现的后端不能报告可用")
	}
	_, err := Prepare(context.Background(), config)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "BACKEND_UNAVAILABLE" {
		t.Fatalf("准备操作应拒绝：%v", err)
	}
	_, err = Run(context.Background(), Request{
		Operation: "exec", Sandbox: config,
		Command: Command{Argv: []string{"cmd.exe", "/c", "echo", "不应执行"}, Cwd: "workspace", TimeoutMs: 1000},
	}, func(string, []byte) error { t.Error("不应产生任何命令输出"); return nil })
	if !errors.As(err, &typed) || typed.Code != "BACKEND_UNAVAILABLE" {
		t.Fatalf("执行操作应拒绝：%v", err)
	}
}
