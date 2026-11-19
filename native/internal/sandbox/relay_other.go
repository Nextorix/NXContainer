//go:build !linux

/*
 * NXContainer Network Relay（非 Linux）
 *
 * 只有 Linux 联网沙箱经沙箱内中继与宿主侧代理访问公网；其他平台明确拒绝。
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
	"fmt"
	"os"
)

func NetRelay([]string) int {
	fmt.Fprintln(os.Stderr, "nxcontainer：当前平台不使用网络中继")
	return 2
}
