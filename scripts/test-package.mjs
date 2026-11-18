/*
 * NXContainer Package Acceptance
 *
 * 实际打包并离线安装到仓库外的临时应用，验证依赖库分发而非源码相对导入。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { access, copyFile, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import os from 'node:os';
import path from 'node:path';

const root = fileURLToPath(new URL('..', import.meta.url));
const npm = process.env.npm_execpath;
if (!npm || !path.isAbsolute(npm)) throw new Error('请通过 npm run test:package 执行');
for (const target of ['win32-x64.exe', 'win32-arm64.exe', 'linux-x64', 'linux-arm64']) {
  try { await access(path.join(root, 'bin', `nxcontainer-${target}`)); }
  catch { throw new Error('缺少分发产物，请先运行 npm run build:all'); }
}
const temp = await realpath(await mkdtemp(path.join(os.tmpdir(), 'nx-package-')));
const application = path.join(temp, 'consumer application');
const cache = path.join(temp, 'npm-cache');
const slow = process.env.NXCONTAINER_TEST_SLOW === '1';
function run(args, cwd, withoutToolchain = false) {
  // 保持宿主 NODE_OPTIONS 不可注入；TCG 模式只用固定的解释执行选项。
  const env = { ...process.env, NODE_PATH: '', NODE_OPTIONS: slow ? '--jitless' : '' };
  if (withoutToolchain) {
    for (const key of Object.keys(env)) if (key.toUpperCase() === 'PATH') delete env[key];
    env.PATH = '';
  }
  const result = spawnSync(process.execPath, args, {
    cwd, encoding: 'utf8', timeout: slow ? 600_000 : 120_000, maxBuffer: 4 * 1024 * 1024, env,
  });
  if (result.error || result.status !== 0) {
    const step = withoutToolchain ? '消费端' : `npm ${args[1]}`;
    throw new Error(`依赖库验收失败（${step}）：${result.error?.message ?? `退出码 ${result.status}`}\n${result.stderr || result.stdout}`, { cause: result.error });
  }
  return result.stdout;
}
try {
  const [packed] = JSON.parse(run([npm, 'pack', '--ignore-scripts', '--json', '--pack-destination', temp, '--cache', cache], root));
  const contents = new Set(packed.files.map((file) => file.path));
  for (const required of ['src/index.js', 'src/index.d.ts', 'src/runtime.js', 'LICENSE', 'licenses/Go-BSD.LICENSE', 'docs/protocol.md']) {
    assert.ok(contents.has(required), `分发包缺少 ${required}`);
  }
  for (const name of contents) assert.ok(!/^(data|gateway|webui|server|node_modules)\//.test(name), `分发包不应包含宿主内容：${name}`);
  await mkdir(application);
  await writeFile(path.join(application, 'package.json'), JSON.stringify({ name: 'nxcontainer-consumer-fixture', private: true, type: 'module' }));
  run([npm, 'install', path.join(temp, packed.filename), '--offline', '--ignore-scripts', '--no-audit', '--no-fund', '--no-bin-links', '--package-lock=false', '--cache', cache], application);
  const installed = path.join(application, 'node_modules', 'nxcontainer');
  const manifest = JSON.parse(await readFile(path.join(installed, 'package.json'), 'utf8'));
  assert.equal(manifest.types, './src/index.d.ts');
  assert.equal(manifest.sideEffects, false);
  assert.equal(Object.keys(manifest.dependencies ?? {}).length, 0);
  assert.equal(Object.keys(manifest.optionalDependencies ?? {}).length, 0);
  for (const lifecycle of ['preinstall', 'install', 'postinstall', 'prepare']) assert.equal(manifest.scripts?.[lifecycle], undefined);
  await access(path.join(installed, manifest.types));
  await copyFile(path.join(root, 'test', 'fixtures', 'package-consumer.mjs'), path.join(application, 'consumer.mjs'));
  process.stdout.write(run(['consumer.mjs'], application, true));
  console.log('依赖库打包、离线安装和仓库外导入验收通过');
} finally {
  await rm(temp, { recursive: true, force: true });
}
