//go:build !linux && !windows

/*
 * NXContainer Unsupported Backend
 *
 * 对尚未实现的平台明确拒绝执行，不降级为宿主进程。
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
	"io"
	"runtime"
)

// 未实现的平台必须明确拒绝，不能使用普通宿主进程伪装隔离。
func Probe(context.Context) Capabilities {
	return Capabilities{Platform: runtime.GOOS, Architecture: runtime.GOARCH, Backend: "unsupported", Status: "unavailable", Validation: "unsupported", Reason: "当前平台不受支持"}
}

func runPlatform(context.Context, Config, Command, io.Writer, io.Writer) (int, error) {
	return -1, problem("BACKEND_UNAVAILABLE", Probe(context.Background()).Reason)
}

func validatePlatform(Config) error { return nil }
