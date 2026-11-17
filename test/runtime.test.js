/*
 * NXContainer Runtime Integration Tests
 *
 * 验证真实平台能力、隔离执行、只读共享与进程树清理。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import test from 'node:test';
import assert from 'node:assert/strict';
import os from 'node:os';
import path from 'node:path';
import { access, mkdtemp, mkdir, rm, writeFile, readFile, realpath } from 'node:fs/promises';
import { NXContainer } from '../src/index.js';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';

async function setup(t) {
  const root = await realpath(await mkdtemp(path.join(os.tmpdir(), 'nx-integration-')));
  const nx = new NXContainer({ allowedRoots: [root], allowExperimentalLinux: true, allowExperimentalWindows: true });
  t.after(async () => { await nx.close(); await rm(root, { recursive: true, force: true }); });
  return { root, nx };
}

const config = (agentId, source, extra = []) => ({ agentId, mounts: [{ name: 'workspace', source, access: 'read-write' }, ...extra] });

async function capability(t, nx) {
  let value;
  try { value = await nx.capabilities(); }
  catch (error) {
    if (error.code !== 'RUNTIME_UNAVAILABLE' || process.env.NXCONTAINER_REQUIRE_RUNTIME === '1' || process.env.NXCONTAINER_REQUIRE_LINUX === '1') throw error;
    t.skip('执行器尚未构建，请先 npm run build');
    return null;
  }
  return value;
}

test('本机执行器报告真实能力；不支持的平台不能触发宿主执行', async (t) => {
  const { root, nx } = await setup(t);
  const capabilities = await capability(t, nx);
  if (!capabilities) return;
  const expectedValidation = { win32: { x64: 'partial', arm64: 'pending' }, linux: { x64: 'pending', arm64: 'pending' } };
  assert.equal(capabilities.validation, expectedValidation[process.platform]?.[process.arch] ?? 'unsupported');
  assert.equal(capabilities.desktop, false);
  assert.equal(capabilities.resourceQuotas, false);
  if (!capabilities.exec) {
    await assert.rejects(nx.createSandbox(config('a', root)), { code: 'BACKEND_UNAVAILABLE' });
    await assert.rejects(access(path.join(root, 'should-not-exist')));
  }
  if (process.platform === 'win32') {
    assert.equal(capabilities.backend, 'windows-lpac');
    assert.equal(capabilities.exec, true, capabilities.reason);
    assert.equal(capabilities.processQuotas, true);
    assert.equal(capabilities.desktopIsolation, true);
  }
});

test('Linux 待验证：原生隔离、只读共享、文件接口、取消与输出限制', async (t) => {
  const { root, nx } = await setup(t);
  if (process.platform !== 'linux') {
    if (process.env.NXCONTAINER_REQUIRE_LINUX === '1') assert.fail('Linux 验收必须在真实 Linux 测试机运行');
    return t.skip('待验证：需要真实 Linux 内核与 bubblewrap');
  }
  const capabilities = await capability(t, nx);
  if (!capabilities) return;
  if (!capabilities.exec) {
    if (process.env.NXCONTAINER_REQUIRE_LINUX === '1') assert.fail(capabilities.reason);
    return t.skip(capabilities.reason);
  }
  const aDir = path.join(root, 'a'), bDir = path.join(root, 'b');
  await mkdir(aDir); await mkdir(bDir);
  await writeFile(path.join(root, 'host-secret'), '宿主秘密');
  const a = await nx.createSandbox(config('a', aDir));
  const b = await nx.createSandbox(config('b', bDir, [{ name: 'peer', source: aDir, access: 'read-only' }]));
  assert.equal(a.paths.workspace, '/Agents/a/workspace');
  await a.writeFile('workspace', 'hello.txt', '你好');
  assert.equal((await b.readFile('peer', 'hello.txt')).toString(), '你好');
  await assert.rejects(b.writeFile('peer', 'hello.txt', '非法修改'), { code: 'PERMISSION_DENIED' });
  const denied = await b.exec(['/bin/sh', '-c', 'printf attack > ../peer/hello.txt']);
  assert.notEqual(denied.exitCode, 0);
  assert.equal(await readFile(path.join(aDir, 'hello.txt'), 'utf8'), '你好');
  await assert.rejects(nx.createSandbox(config('c', aDir)), { code: 'MOUNT_CONFLICT' });

  const hidden = await a.exec(['/bin/sh', '-c', 'test ! -e "$1" && test ! -e /etc/passwd && test ! -e /Agents/b && test ! -e /proc/1/root/etc/passwd', 'sh', path.join(root, 'host-secret')]);
  assert.equal(hidden.exitCode, 0, hidden.stderr);
  const marker = 'NX_TEST_HOST_SECRET';
  const previous = process.env[marker];
  try {
    process.env[marker] = '不应继承';
    assert.equal((await a.exec(['/bin/sh', '-c', `test -z "$${marker}"`])).exitCode, 0);
  } finally {
    if (previous === undefined) delete process.env[marker]; else process.env[marker] = previous;
  }
  const systemWrite = await a.exec(['/bin/sh', '-c', 'touch /usr/nx-should-not-exist']);
  assert.notEqual(systemWrite.exitCode, 0);
  await a.exec(['/bin/sh', '-c', 'ln -s "$1" escape', 'sh', root]);
  await assert.rejects(a.readFile('workspace', 'escape/host-secret'));
  await assert.rejects(a.writeFile('workspace', 'escape/host-secret', '不能写'));
  assert.equal(await readFile(path.join(root, 'host-secret'), 'utf8'), '宿主秘密');

  await a.mkdir('workspace', 'empty');
  assert.deepEqual(await a.listFiles('workspace', 'empty'), []);
  await a.writeFile('workspace', 'empty/blank', '');
  assert.equal((await a.readFile('workspace', 'empty/blank')).length, 0);
  await a.removeFile('workspace', 'empty/blank');

  const running = a.exec(['/bin/sh', '-c', '(sleep 1; touch orphan) & wait'], { timeoutMs: 200 });
  await assert.rejects(a.exec(['/bin/true']), { code: 'AGENT_BUSY' });
  await assert.rejects(running, { code: 'TIMEOUT' });
  await new Promise((resolve) => setTimeout(resolve, 1200));
  await assert.rejects(access(path.join(aDir, 'orphan')));

  await assert.rejects(a.exec(['/bin/sh', '-c', 'while :; do printf 1234567890; done']), { code: 'OUTPUT_LIMIT' });
  const socket = await a.exec(['/usr/bin/python3', '-c', 'import socket\ntry: socket.socket(socket.AF_UNIX)\nexcept PermissionError: exit(0)\nexit(1)']);
  assert.equal(socket.exitCode, 0, socket.stderr);

  await b.dispose(); await a.dispose();
  const again = await nx.createSandbox(config('a', aDir));
  await a.dispose();
  await assert.rejects(nx.createSandbox(config('a', bDir)), { code: 'AGENT_EXISTS' });
  await again.dispose();

  const parent = spawn(process.execPath, [fileURLToPath(new URL('./fixtures/owner.mjs', import.meta.url)), bDir], { stdio: ['ignore', 'pipe', 'pipe'] });
  const exited = once(parent, 'close');
  t.after(() => { if (parent.exitCode === null) parent.kill('SIGKILL'); });
  let diagnostics = '';
  parent.stderr.on('data', (data) => { diagnostics += data.toString().slice(0, 4096 - diagnostics.length); });
  await new Promise((resolve, reject) => {
    let output = '';
    const timer = setTimeout(() => reject(new Error(`测试父进程未就绪：${diagnostics}`)), 10000);
    parent.once('error', (error) => { clearTimeout(timer); reject(error); });
    parent.once('close', () => { clearTimeout(timer); reject(new Error(`测试父进程提前退出：${diagnostics}`)); });
    parent.stdout.on('data', (data) => {
      output += data.toString();
      if (output.includes('READY')) { clearTimeout(timer); resolve(); }
    });
  });
  parent.kill('SIGKILL');
  await exited;
  await new Promise((resolve) => setTimeout(resolve, 2500));
  await assert.rejects(access(path.join(bDir, 'escaped-after-parent')));
});
