/*
 * NXContainer Parent Lifetime Fixture
 *
 * 为集成测试创建任务，验证可信父进程退出后的沙箱清理。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Linux 集成测试：测试方会杀死此可信父进程，检查其沙箱是否同步清理。
import { NXContainer } from '../../src/index.js';
const workspace = process.argv[2];
const nx = new NXContainer({ allowedRoots: [workspace], allowExperimentalLinux: true });
try {
  const sandbox = await nx.createSandbox({ agentId: 'owner-test', mounts: [{ name: 'workspace', source: workspace, access: 'read-write' }] });
  await sandbox.exec(['/bin/sh', '-c', 'printf READY; sleep 2; touch escaped-after-parent'], {
    onOutput: ({ stream, data }) => { if (stream === 'stdout') process.stdout.write(data); },
  });
} finally {
  await nx.close();
}
