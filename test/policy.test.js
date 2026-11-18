/*
 * NXContainer Permission Policy Tests
 *
 * 验证目录授权、共享规则、不可变策略与路径越界拒绝。
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
import path from 'node:path';
import os from 'node:os';
import { mkdtemp, mkdir, rm, symlink } from 'node:fs/promises';
import { assertSharing, directory, filePath, normalizePolicy } from '../src/policy.js';
import { NXContainer } from 'nxcontainer';

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'nx-policy-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const a = path.join(root, 'a'), b = path.join(root, 'b');
  await mkdir(a); await mkdir(b);
  return { root: await directory(root), a, b };
}
const config = (agentId, source) => ({ agentId, mounts: [{ name: 'workspace', source, access: 'read-write' }] });

test('策略是不可变快照，不修改宿主权限', async (t) => {
  const { root, a } = await fixture(t);
  const input = config('agent-a', a);
  const result = await normalizePolicy(input, [root], true);
  input.mounts[0].access = 'read-only';
  assert.equal(result.mounts[0].access, 'read-write');
  assert.ok(Object.isFrozen(result.mounts[0]));
  assert.equal(result.network, 'none');
  assert.equal(result.desktop, false);
});

test('拒绝未知能力、重复挂载、越界目录及无效身份', async (t) => {
  const { root, a, b } = await fixture(t);
  for (const invalid of [
    { ...config('a', a), agentId: '../b' },
    { ...config('a', a), agentId: 123 },
    { ...config('a', a), mounts: [{ name: 123, source: a, access: 'read-write' }] },
    { ...config('a', a), network: 'host' },
    { ...config('a', a), desktop: true },
    { ...config('a', a), privileged: true },
    { ...config('a', a), limits: { memoryMb: 1024 } },
    { ...config('a', a), mounts: [{ name: 'reference', source: a, access: 'read-only' }] },
    { ...config('a', a), mounts: [{ name: 'workspace', source: a, access: 'read-write' }, { name: 'alias', source: a, access: 'read-only' }] },
  ]) await assert.rejects(normalizePolicy(invalid, [root], true));
  await assert.rejects(normalizePolicy(config('a', b), [a], true), { code: 'PERMISSION_DENIED' });
  await assert.rejects(directory(path.parse(a).root), { code: 'INVALID_CONFIG' });
});

test('工作区允许只读，策略不会隐式提升权限', async (t) => {
  const { root, a } = await fixture(t);
  const input = config('reader', a);
  input.mounts[0].access = 'read-only';
  const policy = await normalizePolicy(input, [root], true);
  assert.equal(policy.mounts[0].access, 'read-only');
  assert.ok(Object.isFrozen(policy.mounts[0]));
});

test('进程配额验证范围并保持不可变', async (t) => {
  const { root, a } = await fixture(t);
  const limits = { memoryBytes: 128 * 1024 * 1024, maxProcesses: 2, cpuPercent: 25 };
  const policy = await normalizePolicy({ ...config('a', a), limits }, [root], true);
  limits.cpuPercent = 100;
  assert.equal(policy.limits.cpuPercent, 25);
  assert.ok(Object.isFrozen(policy.limits));
  for (const invalid of [
    { memoryBytes: 1 }, { memoryBytes: 8 * 1024 * 1024 * 1024 + 1 },
    { maxProcesses: 0 }, { maxProcesses: 257 }, { cpuPercent: 0 }, { cpuPercent: 101 },
  ]) await assert.rejects(normalizePolicy({ ...config('a', a), limits: invalid }, [root], true), { code: 'INVALID_CONFIG' });
});

test('写权限归属唯一；允许显式只读共享完整挂载根', async (t) => {
  const { root, a, b } = await fixture(t);
  const owner = await normalizePolicy(config('a', a), [root], true);
  const writable = await normalizePolicy(config('b', a), [root], true);
  assert.throws(() => assertSharing(writable, [owner]), { code: 'MOUNT_CONFLICT' });
  const reader = await normalizePolicy({ ...config('b', b), mounts: [
    ...config('b', b).mounts, { name: 'peer', source: a, access: 'read-only' },
  ] }, [root], true);
  assert.doesNotThrow(() => assertSharing(reader, [owner]));
  assert.doesNotThrow(() => assertSharing(owner, [reader]));
  const nested = path.join(a, 'nested'); await mkdir(nested);
  const unsafe = await normalizePolicy({ ...config('b', b), mounts: [
    ...config('b', b).mounts, { name: 'peer', source: nested, access: 'read-only' },
  ] }, [root], true);
  assert.throws(() => assertSharing(unsafe, [owner]), { code: 'MOUNT_CONFLICT' });
});

test('拒绝目录联接和符号链接源', async (t) => {
  const { root, a } = await fixture(t);
  const linked = path.join(root, 'linked');
  await symlink(a, linked, process.platform === 'win32' ? 'junction' : 'dir');
  await assert.rejects(directory(linked), { code: 'UNSAFE_PATH' });
});

test('跨平台文件路径检查', () => {
  for (const invalid of ['/etc/passwd', '../a', 'workspace/../../x', 'C:/Windows', 'a\\b', 'a\0b']) {
    assert.throws(() => filePath(invalid), { code: 'INVALID_PATH' });
  }
  assert.equal(filePath('文档/a.txt'), '文档/a.txt');
});

test('没有隐式目录授权或 shell 后备', async () => {
  assert.throws(() => new NXContainer({ allowedRoots: [] }), { code: 'INVALID_CONFIG' });
  assert.throws(() => new NXContainer({ allowedRoots: [os.tmpdir()], fallback: 'shell' }), { code: 'INVALID_CONFIG' });
  const nx = new NXContainer({ allowedRoots: [os.tmpdir()], runtimePath: path.join(os.tmpdir(), 'nx-not-installed', 'runtime') });
  await assert.rejects(nx.capabilities(), { code: 'RUNTIME_UNAVAILABLE' });
  await nx.close();
  await assert.rejects(nx.capabilities(), { code: 'CLOSED' });
});
