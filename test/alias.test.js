/*
 * NXContainer Windows 显示联接与只读工作区验证。
 * Author: MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { NXContainer } from 'nxcontainer';

test('Windows 显示联接：文件与命令可用，父目录及其他 Agent 不可读写，重新授权只读生效', { skip: process.platform !== 'win32' }, async (t) => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'nx-alias-'));
  const source = path.join(root, 'source'), peer = path.join(root, 'peer');
  const parent = path.join(root, 'Agents', 'agent-a'), alias = path.join(parent, 'workspace');
  for (const dir of [source, peer, parent]) await fs.mkdir(dir, { recursive: true });
  await fs.writeFile(path.join(parent, 'parent-secret'), '不允许读取');
  await fs.writeFile(path.join(peer, 'secret'), '其他智能体');
  await fs.symlink(source, alias, 'junction');
  const nx = new NXContainer({ allowedRoots: [root], allowExperimentalWindows: true });
  t.after(async () => { await nx.close(); await fs.rm(root, { recursive: true, force: true }); });
  const config = (access) => ({ agentId: 'alias-a', mounts: [{ name: 'workspace', source, alias, access }] });
  const writable = await nx.createSandbox(config('read-write'));
  assert.equal(writable.paths.workspace.toLowerCase(), alias.toLowerCase());
  await writable.writeFile('workspace', 'example.txt', '工作区文件');
  assert.equal((await writable.readFile('workspace', 'example.txt')).toString(), '工作区文件');
  const run = (s, cmd) => s.exec(['cmd.exe', '/d', '/c', cmd]);
  assert.equal((await run(writable, 'echo written>shell.txt')).exitCode, 0);
  assert.notEqual((await run(writable, `type "${path.join(parent, 'parent-secret')}"`)).exitCode, 0);
  assert.notEqual((await run(writable, `type "${path.join(peer, 'secret')}"`)).exitCode, 0);
  assert.notEqual((await run(writable, `echo forbidden>"${path.join(parent, 'escape.txt')}"`)).exitCode, 0);
  await writable.dispose();
  const readonly = await nx.createSandbox(config('read-only'));
  assert.notEqual((await run(readonly, 'echo forbidden>shell.txt')).exitCode, 0);
  // cmd 的 del 在拒绝访问时也可能返回 0；核对错误输出和实际文件，不依赖退出码代理信号。
  assert.ok((await run(readonly, 'del example.txt')).stderr.length > 0);
  assert.equal((await readonly.readFile('workspace', 'example.txt')).toString(), '工作区文件');
  await assert.rejects(readonly.writeFile('workspace', 'a', '不可写'), { code: 'PERMISSION_DENIED' });
  await readonly.dispose();
  // 错误目标、普通目录伪装、未经授权的联接父目录一律拒绝。
  await fs.unlink(alias); await fs.symlink(peer, alias, 'junction');
  await assert.rejects(nx.createSandbox(config('read-write')), { code: 'UNSAFE_PATH' });
  await fs.unlink(alias); await fs.mkdir(alias);
  await assert.rejects(nx.createSandbox(config('read-write')), { code: 'UNSAFE_PATH' });
  await fs.rmdir(alias); await fs.symlink(source, alias, 'junction');
  const restricted = new NXContainer({ allowedRoots: [source], allowExperimentalWindows: true });
  try { await assert.rejects(restricted.createSandbox(config('read-only')), { code: 'PERMISSION_DENIED' }); }
  finally { await restricted.close(); }
});
