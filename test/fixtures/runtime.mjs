/*
 * NXContainer Protocol Test Fixture
 *
 * 模拟传输测试所需的协议事件，不提供命令执行能力。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// 仅用于传输测试的协议对端，不执行命令，也不是隔离后端。
import { createInterface } from 'node:readline';
const reader = createInterface({ input: process.stdin });
const send = (event) => process.stdout.write(`${JSON.stringify({ version: 1, ...event })}\n`);
reader.once('line', (line) => {
  const request = JSON.parse(line);
  if (request.operation === 'hang') return;
  if (request.operation === 'malformed') { process.stdout.write('不是 JSON\n'); return; }
  if (request.operation === 'large-line') { process.stdout.write('x'.repeat(140 * 1024)); return; }
  if (request.operation === 'missing-result') { process.exit(0); }
  if (request.operation === 'error') {
    send({ type: 'error', error: { code: 'BACKEND_UNAVAILABLE', message: '后端不可用' } });
    process.exit(0);
  }
  if (request.operation === 'overflow') {
    send({ type: 'stdout', data: Buffer.alloc(2000).toString('base64') });
    return;
  }
  const data = Buffer.from('中文输出');
  send({ type: 'stdout', data: data.subarray(0, 2).toString('base64') });
  send({ type: 'stdout', data: data.subarray(2).toString('base64') });
  send({ type: 'stderr', data: Buffer.from('错误流').toString('base64') });
  send({ type: 'result', result: { exitCode: 3, durationMs: 1 } });
  if (request.operation === 'after-result') send({ type: 'stdout', data: '' });
  process.exit(0);
});
