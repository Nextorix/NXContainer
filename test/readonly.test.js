/*
 * 只读工作区的真实后端验证：文件 API 与 shell 都不能修改持久工作区。
 * Author: MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import os from 'node:os';
import path from 'node:path';
import { mkdtemp, mkdir, writeFile, readFile, readdir, rm } from 'node:fs/promises';
import { NXContainer } from 'nxcontainer';

test('只读工作区：读取成功，shell 与文件 API 写入失败，释放后可重新授权写入', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'nx-readonly-'));
  const workspace = path.join(root, 'workspace');
  await mkdir(workspace);
  await writeFile(path.join(workspace, 'original.txt'), '原始内容');
  const nx = new NXContainer({ allowedRoots: [root], allowExperimentalWindows: true, allowExperimentalLinux: true });
  t.after(async () => { await nx.close(); await rm(root, { recursive: true, force: true }); });
  const cap = await nx.capabilities();
  if (!cap.exec) {
    if (process.env.NXCONTAINER_REQUIRE_RUNTIME === '1') assert.fail(cap.reason);
    return t.skip(cap.reason);
  }
  const config = (access) => ({ agentId: 'readonly', mounts: [{ name: 'workspace', source: workspace, access }], limits: { timeoutMs: 15000 } });
  const sandbox = await nx.createSandbox(config('read-only'));
  assert.equal((await sandbox.readFile('workspace', 'original.txt')).toString(), '原始内容');
  assert.equal((await sandbox.listFiles('workspace')).length, 1);
  for (const operation of [
    () => sandbox.writeFile('workspace', 'new.txt', '不允许'),
    () => sandbox.mkdir('workspace', 'new'),
    () => sandbox.removeFile('workspace', 'original.txt'),
  ]) await assert.rejects(operation, { code: 'PERMISSION_DENIED' });
  const commands = process.platform === 'win32'
    ? ['echo changed>original.txt', 'echo new>new.txt', 'del original.txt', 'mkdir new', 'ren original.txt renamed.txt']
    : ['echo changed > original.txt', 'echo new > new.txt', 'rm original.txt', 'mkdir new', 'mv original.txt renamed.txt'];
  for (const command of commands) {
    const result = await sandbox.exec(process.platform === 'win32' ? ['cmd.exe', '/d', '/c', command] : ['/bin/sh', '-c', command]);
    assert.notEqual(result.exitCode, 0, command);
    assert.equal(await readFile(path.join(workspace, 'original.txt'), 'utf8'), '原始内容');
    assert.deepEqual(await readdir(workspace), ['original.txt']);
  }
  await sandbox.dispose();
  const writable = await nx.createSandbox(config('read-write'));
  await writable.writeFile('workspace', 'original.txt', '重新授权');
  assert.equal((await writable.readFile('workspace', 'original.txt')).toString(), '重新授权');
});
