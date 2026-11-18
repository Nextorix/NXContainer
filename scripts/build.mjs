/*
 * NXContainer Native Build
 *
 * 构建当前平台或交叉编译指定平台的 Go 原生执行器。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import { spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('..', import.meta.url));
const platform = process.argv[2] ?? process.platform;
if (platform === 'all' && process.argv[3] !== undefined) throw new Error('all 不接受单独的架构参数');
const targets = platform === 'all'
  ? [['win32', 'x64'], ['win32', 'arm64'], ['linux', 'x64'], ['linux', 'arm64']]
  : [[platform, process.argv[3] ?? process.arch]];
mkdirSync(path.join(root, 'bin'), { recursive: true });
for (const [platform, architecture] of targets) {
  const goos = { win32: 'windows', linux: 'linux' }[platform];
  const goarch = { x64: 'amd64', arm64: 'arm64' }[architecture];
  if (!goos || !goarch) throw new Error('只支持 win32/linux 与 x64/arm64');
  const output = path.join(root, 'bin', `nxcontainer-${platform}-${architecture}${platform === 'win32' ? '.exe' : ''}`);
  const result = spawnSync('go', ['build', '-trimpath', '-o', output, './cmd/nxcontainer'], {
    cwd: path.join(root, 'native'),
    env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: goarch },
    stdio: 'inherit',
  });
  if (result.error) throw new Error('无法执行 Go，请安装 Go 1.25 或更新版本', { cause: result.error });
  if (result.status !== 0) process.exit(result.status ?? 1);
  console.log(`已构建：${output}`);
}
