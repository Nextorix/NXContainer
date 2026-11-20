/*
 * NXContainer Windows SDK Integration Tests
 *
 * 使用临时目录和 portable Node.js 验证真实 Windows 隔离与资源限制。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, realpath, rm, readFile, writeFile, copyFile, access, symlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { once } from 'node:events';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { NXContainer } from 'nxcontainer';

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

test('Windows：真实 SDK、双 Agent、只读共享、portable 工具、网络和取消', { timeout: 90_000 }, async (t) => {
  if (process.platform !== 'win32') return t.skip('需要真实 Windows 内核');
  const root = await realpath(await mkdtemp(path.join(os.tmpdir(), 'nx-windows-')));
  const nx = new NXContainer({ allowedRoots: [root], allowExperimentalWindows: true });
  t.after(async () => { await nx.close(); await rm(root, { recursive: true, force: true }); });
  const caps = await nx.capabilities();
  assert.equal(caps.exec, true, caps.reason);
  const aDir = path.join(root, '甲 workspace'), bDir = path.join(root, '乙 workspace');
  await mkdir(aDir); await mkdir(bDir);
  await writeFile(path.join(root, 'host-secret.txt'), '未授权数据');
  await copyFile(process.execPath, path.join(aDir, 'node.exe'));
  const a = await nx.createSandbox({ agentId: 'a', mounts: [{ name: 'workspace', source: aDir, access: 'read-write' }], limits: { timeoutMs: 8000 } });
  const b = await nx.createSandbox({ agentId: 'b', mounts: [
    { name: 'workspace', source: bDir, access: 'read-write' },
    { name: 'peer', source: aDir, access: 'read-only' },
  ] });
  const node = path.join(aDir, 'node.exe');

  await t.test('文件接口和权限', async () => {
    await a.writeFile('workspace', '成果.txt', '你好，NXContainer');
    assert.equal((await b.readFile('peer', '成果.txt')).toString(), '你好，NXContainer');
    await assert.rejects(b.writeFile('peer', '成果.txt', '拒绝'), { code: 'PERMISSION_DENIED' });
    await a.mkdir('workspace', 'empty');
    assert.deepEqual(await a.listFiles('workspace', 'empty'), []);
    await a.writeFile('workspace', 'empty/blank', '');
    assert.equal((await a.readFile('workspace', 'empty/blank')).length, 0);
    await a.removeFile('workspace', 'empty/blank');
    const result = await b.exec([node, '-e', `const fs=require('node:fs');try{fs.writeFileSync(process.argv[1],'attack');process.exit(1)}catch(e){if(!['EACCES','EPERM'].includes(e.code))throw e}`, path.join(aDir, '成果.txt')]);
    assert.equal(result.exitCode, 0, result.stderr);
    assert.equal(await readFile(path.join(aDir, '成果.txt'), 'utf8'), '你好，NXContainer');
    const permissions = await b.exec(['icacls.exe', path.join(aDir, '成果.txt'), '/grant', '*S-1-15-2-2:F']);
    assert.notEqual(permissions.exitCode, 0, '共享 Windows 用户不能利用文件所有者身份提升只读权限');
    await assert.rejects(nx.createSandbox({ agentId: 'conflict', mounts: [{ name: 'workspace', source: aDir, access: 'read-write' }] }), { code: 'MOUNT_CONFLICT' });
  });

  await t.test('未授权文件和环境变量不泄露', async () => {
    const key = 'NX_TEST_PRIVATE_ENV';
    const before = process.env[key];
    process.env[key] = '不能传给 Agent';
    try {
      const result = await a.exec([node, '-e', `const fs=require('node:fs');if(process.env.${key})process.exit(2);try{fs.readFileSync(process.argv[1]);process.exit(1)}catch(e){if(!['EACCES','EPERM'].includes(e.code))throw e}`, path.join(root, 'host-secret.txt')]);
      assert.equal(result.exitCode, 0, result.stderr);
    } finally {
      if (before === undefined) delete process.env[key]; else process.env[key] = before;
    }
    await assert.rejects(a.exec([node, '-e', ''], { env: { SystemRoot: root } }), { code: 'INVALID_COMMAND' });
    await assert.rejects(a.exec([node, '-e', ''], { env: { PATH: root, Path: root } }), { code: 'INVALID_COMMAND' });
  });

  await t.test('子进程继承隔离', async () => {
    const result = await a.exec([node, '-e', `const {spawnSync}=require('node:child_process');const r=spawnSync(process.execPath,['-e',"try{require('node:fs').readFileSync(process.argv[1]);process.exit(1)}catch(e){if(!['EACCES','EPERM'].includes(e.code))throw e}",process.argv[1]],{stdio:'inherit'});if(r.error)throw r.error;if(r.status!==0){console.error(r.stderr);process.exit(1)}`, path.join(root, 'host-secret.txt')]);
    assert.equal(result.exitCode, 0, result.stderr);
  });

  await t.test('禁止连接宿主监听端口', async () => {
    let accepted = false;
    const server = net.createServer((socket) => { accepted = true; socket.destroy(); });
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    try {
      const result = await a.exec([node, '-e', `const s=require('node:net').connect({host:'127.0.0.1',port:Number(process.argv[1])});s.on('connect',()=>process.exit(1));s.on('error',()=>process.exit(0));setTimeout(()=>process.exit(2),2000)`, String(server.address().port)]);
      assert.equal(result.exitCode, 0, result.stderr);
      assert.equal(accepted, false);
    } finally { await new Promise((resolve) => server.close(resolve)); }
  });

  await t.test('取消清理子进程，恢复后仍能使用目录', async () => {
    const controller = new AbortController();
    let ready;
    const started = new Promise((resolve) => { ready = resolve; });
    const pending = a.exec([node, '-e', `require('node:child_process').spawn(process.execPath,['-e',"setTimeout(()=>require('node:fs').writeFileSync('orphan','bad'),1500)"],{stdio:'inherit',detached:true});console.log('READY');setInterval(()=>{},1000)`], {
      signal: controller.signal,
      onOutput: ({ data }) => { if (data.toString().includes('READY')) ready(); },
    });
    const cancelled = assert.rejects(pending, { code: 'ABORTED' });
    await Promise.race([started, pause(5000).then(() => { throw new Error('任务未启动'); })]);
    controller.abort();
    await cancelled;
    await pause(1800);
    await assert.rejects(access(path.join(aDir, 'orphan')));
    await a.writeFile('workspace', 'after-cancel', 'ok');
    assert.equal((await a.readFile('workspace', 'after-cancel')).toString(), 'ok');
  });

  await t.test('未适配的 libuv 全局命名管道不能绕过隔离，会被超时回收', async () => {
    await assert.rejects(a.exec([node, '-e', `require('node:child_process').spawnSync(process.execPath,['-e','process.exit(0)'])`], { timeoutMs: 700 }), { code: 'TIMEOUT' });
  });

  await t.test('超时和输出上限', async () => {
    await assert.rejects(a.exec([node, '-e', 'setInterval(()=>{},1000)'], { timeoutMs: 500 }), { code: 'TIMEOUT' });
    await assert.rejects(a.exec([node, '-e', `process.stdout.write('x'.repeat(2*1024*1024))`]), { code: 'OUTPUT_LIMIT' });
  });

  await t.test('拒绝授权树内的 junction，且不改变外部文件', async () => {
    const junction = path.join(bDir, 'escape');
    await symlink(root, junction, 'junction');
    try { await assert.rejects(b.exec(['cmd.exe', '/d', '/c', 'echo', '禁止运行']), { code: 'UNSAFE_PATH' }); }
    finally { await rm(junction); }
    assert.equal(await readFile(path.join(root, 'host-secret.txt'), 'utf8'), '未授权数据');
  });

  await t.test('父进程强制退出后清理任务', async () => {
    const owner = spawn(process.execPath, [fileURLToPath(new URL('./fixtures/windows-owner.mjs', import.meta.url)), aDir], { stdio: ['ignore', 'pipe', 'pipe'] });
    const ended = once(owner, 'close');
    t.after(() => { if (owner.exitCode === null) owner.kill('SIGKILL'); });
    let diagnostics = '';
    owner.stderr.on('data', (data) => { diagnostics += data.toString(); });
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`测试父进程没有就绪：${diagnostics}`)), 10000);
      owner.once('error', (error) => { clearTimeout(timer); reject(error); });
      owner.once('close', () => { clearTimeout(timer); reject(new Error(`测试父进程提前退出：${diagnostics}`)); });
      owner.stdout.on('data', (data) => { if (data.toString().includes('READY')) { clearTimeout(timer); resolve(); } });
    });
    owner.kill('SIGKILL');
    await ended;
    await pause(2500);
    await assert.rejects(access(path.join(aDir, 'after-parent')));
    await a.writeFile('workspace', 'after-parent-check', 'ok');
  });

  await b.dispose(); await a.dispose();
  const limited = await nx.createSandbox({ agentId: 'limited', mounts: [{ name: 'workspace', source: aDir, access: 'read-write' }], limits: { maxProcesses: 1, memoryBytes: 128*1024*1024, cpuPercent: 25 } });
  await t.test('进程数量硬限制', async () => {
    const result = await limited.exec([node, '-e', `const r=require('node:child_process').spawnSync(process.execPath,['-e','process.exit(0)'],{stdio:'inherit'});if(!r.error)process.exit(1)`]);
    assert.equal(result.exitCode, 0, result.stderr);
  });
  await t.test('内存硬限制', async () => {
    assert.equal((await limited.exec([node, '-e', 'process.exit(0)'])).exitCode, 0);
    const result = await limited.exec([node, '-e', `Buffer.alloc(256*1024*1024).fill(1);process.exit(0)`], { timeoutMs: 5000 });
    assert.notEqual(result.exitCode, 0);
  });
  await limited.dispose();
});
