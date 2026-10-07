#!/usr/bin/env node
'use strict';

const path = require('node:path');
const fs = require('node:fs');
const { createHash } = require('node:crypto');
const { spawnSync } = require('node:child_process');

const names = {
  'linux/x64': 'sandboxed-agents-linux-amd64',
  'win32/x64': 'sandboxed-agents-windows-amd64.exe',
};
const platform = `${process.platform}/${process.arch}`;
const name = names[platform];
if (!name) {
  console.error(`unsupported platform ${platform}: sandboxed-agents supports only Linux x64 and Windows x64`);
  process.exit(1);
}
const installing = process.env.npm_lifecycle_event === 'postinstall' && process.env.npm_package_name === 'sandboxed-agents' && process.argv[2] === '--verify-install';
try {
  const checksums = fs.readFileSync(path.join(__dirname, 'SHA256SUMS'), 'utf8');
  const entries = checksums.split(/\r?\n/).map(line => /^([a-f0-9]{64})  (.+)$/.exec(line)).filter(Boolean);
  for (const binary of installing ? Object.values(names) : [name]) {
    const matches = entries.filter(entry => entry[2] === binary);
    if (matches.length !== 1) throw new Error(`SHA256SUMS has no entry for ${binary}`);
    const actual = createHash('sha256').update(fs.readFileSync(path.join(__dirname, binary))).digest('hex');
    if (actual !== matches[0][1]) throw new Error(`checksum mismatch for ${binary}: it does not match SHA256SUMS; reinstall the package`);
  }
} catch (error) {
  console.error(`cannot verify the binary: ${error.message}`);
  process.exit(1);
}
function packagePaths() {
  const paths = new Set([path.resolve(__filename)]);
  const directories = new Set();
  const roots = new Set([__dirname]);
  const raw = path.resolve(process.argv[1]);
  try {
    if (fs.realpathSync(raw) === fs.realpathSync(__filename)) {
      paths.add(raw);
      directories.add(path.dirname(raw));
      if (path.basename(raw) === 'launcher.cjs') roots.add(path.dirname(raw));
    }
  } catch {}
  for (const root of roots) {
    const modules = path.dirname(root);
    if (path.basename(modules) !== 'node_modules') continue;
    directories.add(path.join(modules, '.bin'));
    const prefix = path.dirname(modules);
    if (process.platform === 'win32') directories.add(prefix);
    else if (path.basename(prefix) === 'lib') directories.add(path.join(path.dirname(prefix), 'bin'));
  }
  const packageDirectories = new Set(directories);
  const target = fs.realpathSync(__filename);
  for (const directory of (process.env.PATH || '').split(path.delimiter)) {
    if (directory) directories.add(path.resolve(directory));
  }
  for (const directory of directories) {
    for (const suffix of packageDirectories.has(directory) ? ['', '.cmd', '.ps1'] : []) {
      const candidate = path.join(directory, `sandboxed-agents${suffix}`);
      try { fs.lstatSync(candidate); paths.add(candidate); } catch {}
    }
    let entries;
    try { entries = fs.readdirSync(directory, { withFileTypes: true }); } catch { continue; }
    for (const entry of entries) {
      if (!entry.isSymbolicLink()) continue;
      const candidate = path.join(directory, entry.name);
      try {
        if (fs.realpathSync(candidate) === target) paths.add(candidate);
      } catch {}
    }
  }
  return [...paths];
}

if (!installing) {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => key.toUpperCase() !== 'SANDBOXED_AGENTS_NPM_PATHS'));
  env.SANDBOXED_AGENTS_NPM_PATHS = JSON.stringify(packagePaths());
  const result = spawnSync(path.join(__dirname, name), process.argv.slice(2), { stdio: 'inherit', env });
  if (result.error) {
    console.error(`cannot start the binary: ${result.error.message}`);
    process.exit(1);
  }
  if (result.signal) process.kill(process.pid, result.signal);
  else process.exit(result.status);
}
