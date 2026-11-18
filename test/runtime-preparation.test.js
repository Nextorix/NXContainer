/*
 * NXContainer Runtime Preparation Tests
 *
 * 验证自带执行器的权限准备与失败处理；模拟检查不代替 Linux 实机验收。
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
import { prepareBundledRuntime } from '../src/runtime.js';

function fixture(mode, { regular = true, chmodError } = {}) {
  const calls = [];
  const open = async (name) => {
    calls.push(['open', name]);
    return {
      stat: async () => ({ mode, isFile: () => regular }),
      chmod: async (value) => { calls.push(['chmod', value]); if (chmodError) throw chmodError; },
      close: async () => { calls.push(['close']); },
    };
  };
  return { calls, open, platform: 'linux' };
}

test('Linux 自带执行器只通过已打开句柄补充所有者执行位', async () => {
  const options = fixture(0o640);
  await prepareBundledRuntime('/package/bin/runtime', options);
  assert.deepEqual(options.calls, [['open', '/package/bin/runtime'], ['chmod', 0o740], ['close']]);
  const executable = fixture(0o755);
  await prepareBundledRuntime('/package/bin/runtime', executable);
  assert.deepEqual(executable.calls, [['open', '/package/bin/runtime'], ['close']]);
});

test('Windows 不修改执行位；拒绝非普通文件及提权位', async () => {
  const windows = fixture(0o644);
  await prepareBundledRuntime('C:\\package\\runtime.exe', { ...windows, platform: 'win32' });
  assert.deepEqual(windows.calls, []);
  for (const options of [fixture(0o644, { regular: false }), fixture(0o4755), fixture(0o2755)]) {
    await assert.rejects(prepareBundledRuntime('/package/bin/runtime', options), { code: 'RUNTIME_UNAVAILABLE' });
    assert.deepEqual(options.calls, [['open', '/package/bin/runtime'], ['close']]);
  }
});

test('只读安装目录或符号链接导致明确失败，不下载或执行后备程序', async () => {
  const cause = Object.assign(new Error('只读安装目录'), { code: 'EROFS' });
  const options = fixture(0o644, { chmodError: cause });
  await assert.rejects(prepareBundledRuntime('/package/bin/runtime', options), (error) => {
    assert.equal(error.code, 'RUNTIME_UNAVAILABLE');
    assert.equal(error.cause, cause);
    assert.match(error.message, /runtimePath/);
    return true;
  });
  assert.deepEqual(options.calls.at(-1), ['close']);
  await assert.rejects(prepareBundledRuntime('/package/bin/runtime', {
    platform: 'linux', open: async () => { throw Object.assign(new Error('符号链接被拒绝'), { code: 'ELOOP' }); },
  }), { code: 'RUNTIME_UNAVAILABLE' });
});
