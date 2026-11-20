/*
 * NXContainer Windows Parent Lifetime Fixture
 *
 * 为 Windows 集成测试验证父进程退出后的进程树终止和权限恢复。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import path from 'node:path';
import { NXContainer } from '../../src/index.js';
const workspace = process.argv[2];
const nx = new NXContainer({ allowedRoots: [workspace], allowExperimentalWindows: true });
try {
  const sandbox = await nx.createSandbox({ agentId: 'windows-owner', mounts: [{ name: 'workspace', source: workspace, access: 'read-write' }] });
  await sandbox.exec([path.join(workspace, 'node.exe'), '-e', `console.log('READY');setTimeout(()=>require('node:fs').writeFileSync('after-parent','bad'),2000)`], {
    onOutput: ({ stream, data }) => { if (stream === 'stdout') process.stdout.write(data); },
  });
} finally { await nx.close(); }
