#!/usr/bin/env node
'use strict';

const https = require('https');
const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');
const os = require('os');

const REPO = 'drgould/multi-dev-proxy';
const pkg = require('./package.json');
const VERSION = 'v' + pkg.version;

function getPlatform() {
  const p = process.platform;
  if (p === 'darwin') return 'darwin';
  if (p === 'linux') return 'linux';
  if (p === 'win32') return 'windows';
  throw new Error('Unsupported platform: ' + p);
}

function getArch() {
  const a = process.arch;
  if (a === 'x64') return 'amd64';
  if (a === 'arm64') return 'arm64';
  throw new Error('Unsupported architecture: ' + a);
}

function download(url, dest) {
  return new Promise((resolve, reject) => {
    const file = fs.createWriteStream(dest);
    function get(u) {
      https.get(u, (res) => {
        if (res.statusCode === 301 || res.statusCode === 302) {
          return get(res.headers.location);
        }
        if (res.statusCode !== 200) {
          return reject(new Error('HTTP ' + res.statusCode + ' for ' + u));
        }
        res.pipe(file);
        file.on('finish', () => file.close(resolve));
      }).on('error', reject);
    }
    get(url);
  });
}

async function main() {
  const platform = getPlatform();
  const arch = getArch();
  const ext = platform === 'windows' ? '.zip' : '.tar.gz';
  const archiveName = `mdp_${platform}_${arch}${ext}`;
  const url = `https://github.com/${REPO}/releases/download/${VERSION}/${archiveName}`;

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mdp-'));
  const archivePath = path.join(tmpDir, archiveName);

  console.log(`Downloading mdp ${VERSION} for ${platform}/${arch}...`);
  await download(url, archivePath);

  const binDir = path.join(__dirname, 'bin');
  fs.mkdirSync(binDir, { recursive: true });

  const binaryName = platform === 'windows' ? 'mdp.exe' : 'mdp';
  const binaryDest = path.join(binDir, binaryName);

  if (platform === 'windows') {
    execSync(`powershell -Command "Expand-Archive -Path '${archivePath}' -DestinationPath '${tmpDir}' -Force"`);
  } else {
    execSync(`tar xzf "${archivePath}" -C "${tmpDir}"`);
  }

  fs.copyFileSync(path.join(tmpDir, binaryName), binaryDest);
  if (platform !== 'windows') {
    fs.chmodSync(binaryDest, 0o755);
  }

  fs.rmSync(tmpDir, { recursive: true, force: true });
  console.log('mdp installed successfully.');
}

main().catch((err) => {
  console.error('Failed to install mdp:', err.message);
  process.exit(1);
});
