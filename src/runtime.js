/*
 * NXContainer Bundled Runtime Preparation
 *
 * 为跨平台 npm 分发的自带执行器准备 Linux 执行位，不修改自定义执行器。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import { constants, promises as fs } from 'node:fs';
import { NXContainerError } from './errors.js';

// Windows 打包生成的 tar 可能不含执行位；只对包内自带文件补齐所有者执行位。
// 通过文件句柄操作，不跟随叶子符号链接，也不下载、复制或运行安装脚本。
export async function prepareBundledRuntime(binary, { platform = process.platform, open = fs.open } = {}) {
  if (platform !== 'linux') return;
  try {
    const file = await open(binary, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    try {
      const info = await file.stat();
      if (!info.isFile() || (info.mode & 0o6000)) throw new Error('自带执行器必须是普通文件，且不能有 setuid/setgid 位');
      if (!(info.mode & 0o100)) await file.chmod((info.mode & 0o777) | 0o100);
    } finally {
      await file.close();
    }
  } catch (cause) {
    throw new NXContainerError('RUNTIME_UNAVAILABLE', `无法准备自带执行器 ${binary}；请检查安装目录权限，或通过 runtimePath 指定已有执行权限的本机产物`, { cause });
  }
}
