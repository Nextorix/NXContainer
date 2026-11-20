/*
 * NXContainer Transport Tests
 *
 * 验证协议事件、字节输出、取消、超时与异常处理。
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
import { fileURLToPath } from 'node:url';
import { invoke } from '../src/transport.js';

const args = [fileURLToPath(new URL('./fixtures/runtime.mjs', import.meta.url))];
// TCG 下 Node 子进程冷启动可能超过 5 秒；显式超时用例仍按原值运行。
const fixtureTimeoutMs = process.env.NXCONTAINER_TEST_SLOW === '1' ? 45_000 : 5_000;
const call = (operation, options) => invoke(process.execPath, { operation }, { args, timeoutMs: fixtureTimeoutMs, ...options });

test('流式字节不会破坏 UTF-8，子命令非零退出不是执行器崩溃', async () => {
  const chunks = [];
  const value = await call('success', { onOutput: (event) => chunks.push(event) });
  assert.equal(value.stdout.toString(), '中文输出');
  assert.equal(value.stderr.toString(), '错误流');
  assert.equal(value.result.exitCode, 3);
  assert.equal(chunks.length, 3);
});

test('回调修改收到的字节不影响最终命令输出', async () => {
  const value = await call('success', { onOutput: ({ data }) => data.fill(0) });
  assert.equal(value.stdout.toString(), '中文输出');
  assert.equal(value.stderr.toString(), '错误流');
});

test('严格处理协议错误与输出上限', async () => {
  for (const operation of ['malformed', 'large-line', 'missing-result', 'after-result']) {
    await assert.rejects(call(operation), { code: 'PROTOCOL_ERROR' });
  }
  await assert.rejects(call('overflow', { maxOutputBytes: 1000 }), { code: 'OUTPUT_LIMIT' });
  await assert.rejects(call('error'), { code: 'BACKEND_UNAVAILABLE' });
});

test('预取消、运行取消、超时都会结束执行器', async () => {
  await assert.rejects(call('hang', { signal: AbortSignal.abort() }), { code: 'ABORTED' });
  const controller = new AbortController();
  const active = call('hang', { signal: controller.signal });
  setTimeout(() => controller.abort(), 100);
  await assert.rejects(active, { code: 'ABORTED' });
  await assert.rejects(call('hang', { timeoutMs: 100 }), { code: 'RUNTIME_TIMEOUT' });
});

test('回调异常也终止执行器，不遗留任务', async () => {
  await assert.rejects(call('success', { onOutput: () => { throw new Error('回调异常'); } }), { code: 'PROTOCOL_ERROR' });
});
