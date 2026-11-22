// The npm dependency check of packet FA2-T8 section 8 (FA-0 58, FA-2 242). It prints the
// complete difference between the base lock B and the head lock H and fails on:
// a direct dependency other than the five renderer packages of 6.4, a removed or changed
// existing package path, an install script, a license outside the allowed list, a missing
// integrity, or an added package missing from NOTICE.
//
// Usage: node prototype/tools/deps-check.js <base lock> <head lock> <node_modules dir> <NOTICE>
// (B is `git show <base>:web/package-lock.json`.) Exit 0 = pass, 1 = a finding, 2 = an
// input that cannot be read or parsed.
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

export const ALLOWED_DIRECT = {
  '@xyflow/react': '12.12.1',
  '@dagrejs/dagre': '3.1.3',
  sigma: '4.0.4',
  graphology: '0.26.0',
  'graphology-types': '0.24.8',
};

export const ALLOWED_LICENSES = ['MIT', 'ISC', 'BSD-2-Clause', 'BSD-3-Clause', 'Apache-2.0', '0BSD'];

const ENTRY_FIELDS = ['version', 'resolved', 'integrity', 'license', 'hasInstallScript'];

// noticeHeading is the NOTICE heading line of one added package.
export function noticeHeading(name, version, license) {
  return `### ${name} ${version} (${license})`;
}

// packageName is the npm name of a lock path such as node_modules/@xyflow/react.
export function packageName(path) {
  const i = path.lastIndexOf('node_modules/');
  return path.slice(i + 'node_modules/'.length);
}

function entryLine(path, e) {
  return `${path} ${ENTRY_FIELDS.map((f) => `${f}=${e[f] === undefined ? '-' : e[f]}`).join(' ')}`;
}

// packageFiles returns the copyright lines of the license files of an installed package
// and the names of its NOTICE files.
function packageFiles(dir) {
  const out = { copyright: [], notices: [] };
  if (!existsSync(dir)) {
    return out;
  }
  for (const name of readdirSync(dir).sort()) {
    if (/^(licen[cs]e|copying)/i.test(name)) {
      for (const line of readFileSync(join(dir, name), 'utf8').split(/\r?\n/)) {
        if (/^copyright\b/i.test(line.trim()) && !/copyright (notice|holders)/i.test(line)) {
          out.copyright.push(line.trim());
        }
      }
    }
    if (/^notice/i.test(name)) {
      out.notices.push(name);
    }
  }
  return out;
}

// diffLocks compares two parsed locks and returns { lines, findings }.
export function diffLocks(base, head, { nodeModules, notice }) {
  const lines = [];
  const findings = [];
  for (const field of ['name', 'lockfileVersion']) {
    const same = base[field] === head[field];
    lines.push(`root ${field} base=${base[field]} head=${head[field]}${same ? '' : ' CHANGED'}`);
    if (!same) {
      findings.push(`root ${field} changed`);
    }
  }
  const bRoot = (base.packages ?? {})[''] ?? {};
  const hRoot = (head.packages ?? {})[''] ?? {};
  for (const group of ['dependencies', 'devDependencies']) {
    const b = bRoot[group] ?? {};
    const h = hRoot[group] ?? {};
    for (const name of [...new Set([...Object.keys(b), ...Object.keys(h)])].sort()) {
      if (b[name] === h[name]) {
        continue;
      }
      lines.push(`root ${group} ${name} base=${b[name] ?? '-'} head=${h[name] ?? '-'}`);
      if (b[name] !== undefined) {
        findings.push(`root ${group} ${name} removed or changed`);
      } else if (group !== 'dependencies' || ALLOWED_DIRECT[name] !== h[name]) {
        findings.push(`root ${group} ${name}@${h[name]} is not one of the five packages of 6.4`);
      }
    }
  }
  for (const [name, version] of Object.entries(ALLOWED_DIRECT)) {
    if ((hRoot.dependencies ?? {})[name] !== version) {
      findings.push(`root dependencies ${name} is not ${version}`);
    }
  }
  const bPkgs = base.packages ?? {};
  const hPkgs = head.packages ?? {};
  const paths = [...new Set([...Object.keys(bPkgs), ...Object.keys(hPkgs)])].filter((p) => p !== '').sort();
  let added = 0;
  for (const path of paths) {
    const b = bPkgs[path];
    const h = hPkgs[path];
    if (b && !h) {
      lines.push(`removed ${entryLine(path, b)}`);
      findings.push(`${path} removed`);
      continue;
    }
    if (b && h) {
      const changed = ENTRY_FIELDS.concat(['dev', 'optional', 'peer']).filter((f) => JSON.stringify(b[f]) !== JSON.stringify(h[f]));
      if (changed.length > 0) {
        lines.push(`changed ${path} fields=${changed.join(',')} base: ${entryLine(path, b)} head: ${entryLine(path, h)}`);
        findings.push(`${path} changed (${changed.join(',')})`);
      }
      continue;
    }
    added++;
    lines.push(`added ${entryLine(path, h)}`);
    const name = packageName(path);
    if (h.hasInstallScript) {
      findings.push(`${path} has an install script`);
    }
    if (!ALLOWED_LICENSES.includes(h.license)) {
      findings.push(`${path} license ${h.license} is not allowed`);
    }
    if (!h.integrity) {
      findings.push(`${path} has no integrity`);
    }
    const files = packageFiles(join(nodeModules, path.replace(/^node_modules\//, '')));
    lines.push(`  copyright ${files.copyright.length > 0 ? files.copyright.join(' | ') : '(none in license files)'}`);
    lines.push(`  notice files ${files.notices.length > 0 ? files.notices.join(',') : '(none)'}`);
    if (!notice.split(/\r?\n/).includes(noticeHeading(name, h.version, h.license))) {
      findings.push(`${path} missing from NOTICE (want the line "${noticeHeading(name, h.version, h.license)}")`);
    }
  }
  lines.push(`summary added=${added} findings=${findings.length}`);
  return { lines, findings };
}

function main(argv) {
  if (argv.length !== 4) {
    console.log('usage: deps-check.js <base lock> <head lock> <node_modules dir> <NOTICE>');
    return 2;
  }
  let base;
  let head;
  let notice;
  try {
    base = JSON.parse(readFileSync(argv[0], 'utf8'));
    head = JSON.parse(readFileSync(argv[1], 'utf8'));
    notice = readFileSync(argv[3], 'utf8');
  } catch (err) {
    console.log(`DEPS-CHECK ERROR ${err.message}`);
    return 2;
  }
  const { lines, findings } = diffLocks(base, head, { nodeModules: argv[2], notice });
  for (const l of lines) {
    console.log(`DEPS ${l}`);
  }
  for (const f of findings) {
    console.log(`DEPS-CHECK FAIL ${f}`);
  }
  console.log(`DEPS-CHECK ${findings.length === 0 ? 'PASS' : 'FAIL'}`);
  return findings.length === 0 ? 0 : 1;
}

if (process.argv[1] && process.argv[1].endsWith('deps-check.js')) {
  process.exitCode = main(process.argv.slice(2));
}
