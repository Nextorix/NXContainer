/*
 * NXContainer Temporary Workspace Example
 *
 * 使用临时目录演示两个 Agent 的独立工作区与显式只读共享。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// 只使用临时目录；不能用消费应用的真实业务数据做安全实验。
import os from 'node:os';
import path from 'node:path';
import { mkdtemp, mkdir, realpath, rm } from 'node:fs/promises';
import { NXContainer } from 'nxcontainer';

const root = await realpath(await mkdtemp(path.join(os.tmpdir(), 'nx-example-')));
const nx = new NXContainer({ allowedRoots: [root], allowExperimentalLinux: true, allowExperimentalWindows: true });
try {
  const capabilities = await nx.capabilities();
  console.log('当前能力：', capabilities);
  if (capabilities.validation === 'pending') console.log('此平台待验证，以下仅为临时目录示例，不代表已完成验收');
  if (!capabilities.exec) throw new Error(capabilities.reason);
  const aDir = path.join(root, 'a'), bDir = path.join(root, 'b');
  await mkdir(aDir); await mkdir(bDir);
  const a = await nx.createSandbox({ agentId: 'agent-a', mounts: [{ name: 'workspace', source: aDir, access: 'read-write' }] });
  const b = await nx.createSandbox({ agentId: 'agent-b', mounts: [
    { name: 'workspace', source: bDir, access: 'read-write' },
    { name: 'peer', source: aDir, access: 'read-only' },
  ] });
  await a.writeFile('workspace', '成果.txt', '来自 Agent A 的成果');
  console.log('Agent B 只读查看：', (await b.readFile('peer', '成果.txt')).toString());
  const command = process.platform === 'win32' ? ['cmd.exe', '/d', '/c', 'echo', 'NXContainer'] : ['/bin/sh', '-c', 'pwd; ls ../peer'];
  console.log('命令结果：', await b.exec(command));
  try { await b.writeFile('peer', '成果.txt', '不允许的修改'); }
  catch (error) { console.log('预期拒绝：', error.code, error.message); }
} catch (error) {
  console.error('示例未完成：', error.message);
  process.exitCode = 1;
} finally {
  await nx.close();
  await rm(root, { recursive: true, force: true });
}
