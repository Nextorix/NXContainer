/*
 * NXContainer External Consumer Fixture
 *
 * 仅通过安装后的公开包入口验证能力报告、命令、文件、共享和释放。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import assert from 'node:assert/strict';
import path from 'node:path';
import { mkdir, realpath, readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { NXContainer, NXContainerError, protocolVersion } from 'nxcontainer';

assert.equal(protocolVersion, 1);
assert.equal(fileURLToPath(import.meta.resolve('nxcontainer')), path.resolve('node_modules/nxcontainer/src/index.js'));
await mkdir('workspaces/a', { recursive: true });
await mkdir('workspaces/b', { recursive: true });
const root = await realpath('workspaces');
const nx = new NXContainer({ allowedRoots: [root], allowExperimentalWindows: true, allowExperimentalLinux: true });
try {
  const capabilities = await nx.capabilities();
  const expected = process.platform === 'linux' || process.arch === 'arm64' ? 'pending' : 'partial';
  assert.equal(capabilities.validation, expected);
  assert.equal(capabilities.desktop, false);
  const config = { agentId: 'external-a', mounts: [{ name: 'workspace', source: path.join(root, 'a'), access: 'read-write' }] };
  if (!capabilities.exec) {
    assert.ok(process.platform !== 'win32' && process.env.NXCONTAINER_REQUIRE_LINUX !== '1', capabilities.reason);
    await assert.rejects(nx.createSandbox(config), { code: 'BACKEND_UNAVAILABLE' });
    console.log('包导入及不可用报告通过，未执行隔离任务：', capabilities.reason);
  } else {
    const a = await nx.createSandbox(config);
    const b = await nx.createSandbox({ agentId: 'external-b', mounts: [
      { name: 'workspace', source: path.join(root, 'b'), access: 'read-write' },
      { name: 'reference', source: path.join(root, 'a'), access: 'read-only' },
    ] });
    await a.writeFile('workspace', '说明.txt', '来自独立消费应用');
    assert.equal((await b.readFile('reference', '说明.txt')).toString(), '来自独立消费应用');
    await assert.rejects(b.writeFile('reference', '说明.txt', '禁止修改'), (error) => error instanceof NXContainerError && error.code === 'PERMISSION_DENIED');
    const binary = Buffer.alloc(1024 * 1024);
    for (let i = 0; i < binary.length; i++) binary[i] = i & 255;
    await a.writeFile('workspace', '边界.bin', binary);
    assert.deepEqual(await b.readFile('reference', '边界.bin'), binary);
    await assert.rejects(a.writeFile('workspace', '边界.bin', Buffer.alloc(binary.length + 1)), { code: 'FILE_TOO_LARGE' });
    const command = process.platform === 'win32' ? ['cmd.exe', '/d', '/c', 'echo', 'NX-PACKAGE-OK'] : ['/bin/sh', '-c', 'printf NX-PACKAGE-OK'];
    const output = [];
    const result = await a.exec(command, { onOutput: ({ data }) => output.push(data) });
    assert.equal(result.exitCode, 0, result.stderr);
    assert.match(result.stdout, /NX-PACKAGE-OK/);
    assert.match(Buffer.concat(output).toString(), /NX-PACKAGE-OK/);
    await b.dispose(); await a.dispose();
    await assert.rejects(a.exec(command), { code: 'CLOSED' });
    assert.equal(await readFile(path.join(root, 'a', '说明.txt'), 'utf8'), '来自独立消费应用');
    console.log('安装包命令、文件、只读共享、输出和释放验证通过；平台验证状态：', capabilities.validation);
  }
} finally { await nx.close(); }
await nx.close();
await assert.rejects(nx.capabilities(), { code: 'CLOSED' });
