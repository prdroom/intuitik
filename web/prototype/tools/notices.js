// The third-party notices of the prototype build (packet FA2-T8 E-NOTICE, hoan 59d7f40e and
// 199ae30b). The build lists every module of every final JS and CSS file with its owner (an
// npm package path of the lock, `own` for the repository's sources, or `virtual` with the
// plugin that made it), writes the license files of the notice set byte for byte into one
// notice file, and fails when a file differs from its committed copy under
// web/prototype/notices. The notice set is the fixed 30 non-dev package paths of the lock plus
// every package the module list finds outside them.
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { isAbsolute, join, relative, sep } from 'node:path';

// NOTICE_SET is the fixed set: the 27 package paths FA2-T8 added and react, react-dom and
// scheduler, the 30 non-dev package paths of the lock.
export const NOTICE_SET = [
  'node_modules/@dagrejs/dagre',
  'node_modules/@dagrejs/graphlib',
  'node_modules/@types/d3-color',
  'node_modules/@types/d3-drag',
  'node_modules/@types/d3-interpolate',
  'node_modules/@types/d3-selection',
  'node_modules/@types/d3-transition',
  'node_modules/@types/d3-zoom',
  'node_modules/@xyflow/react',
  'node_modules/@xyflow/system',
  'node_modules/classcat',
  'node_modules/d3-color',
  'node_modules/d3-dispatch',
  'node_modules/d3-drag',
  'node_modules/d3-ease',
  'node_modules/d3-interpolate',
  'node_modules/d3-selection',
  'node_modules/d3-timer',
  'node_modules/d3-transition',
  'node_modules/d3-zoom',
  'node_modules/events',
  'node_modules/graphology',
  'node_modules/graphology-types',
  'node_modules/graphology-utils',
  'node_modules/react',
  'node_modules/react-dom',
  'node_modules/scheduler',
  'node_modules/sigma',
  'node_modules/use-sync-external-store',
  'node_modules/zustand',
];

// LICENSE_FILE matches the license files of a package: LICENSE, LICENCE, COPYING or NOTICE in
// any case, with or without extensions such as .txt or .md.
export const LICENSE_FILE = /^(licen[cs]e|copying|notice)(\.[A-Za-z0-9]+)*$/i;

// VIRTUAL_ORIGINS names the plugin that makes each virtual module id the build reports.
const VIRTUAL_ORIGINS = [
  ['\0vite/', 'vite'],
  ['\0rolldown/', 'rolldown'],
  ['\0virtual:design-system-tokens.css', 'intuitik-design-system-tokens'],
];

export const NOTICE_FILE = 'THIRD_PARTY_LICENSES.txt';
export const MODULES_FILE = 'modules.json';

const byCodeUnit = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

// packagePath returns the lock path of the package that holds a path relative to web/
// (node_modules/<name> or node_modules/@<scope>/<name>, nested packages included), or null.
export function packagePath(rel) {
  const parts = rel.split('/');
  let end = -1;
  for (let i = 0; i < parts.length; i++) {
    if (parts[i] === 'node_modules' && i + 1 < parts.length) {
      end = parts[i + 1].startsWith('@') ? i + 2 : i + 1;
      i = end;
    } else if (end >= 0) {
      break;
    }
  }
  return end < 0 || end >= parts.length ? null : parts.slice(0, end + 1).join('/');
}

// classify returns { id, owner, origin? } for a module id the build reports; owner is null
// when the module cannot be classified.
export function classify(id, webDir) {
  for (const [prefix, origin] of VIRTUAL_ORIGINS) {
    if (id.startsWith(prefix)) {
      return { id, owner: 'virtual', origin };
    }
  }
  const q = id.indexOf('?');
  const file = q < 0 ? id : id.slice(0, q);
  const query = q < 0 ? '' : id.slice(q);
  if (!isAbsolute(file)) {
    return { id, owner: null };
  }
  const rel = relative(webDir, file).split(sep).join('/');
  if (rel.startsWith('../') || isAbsolute(rel)) {
    return { id, owner: null };
  }
  const shown = `${rel}${query}`;
  if (rel.startsWith('node_modules/')) {
    const pkg = packagePath(rel);
    return { id: shown, owner: pkg };
  }
  if (rel.startsWith('prototype/') || rel.startsWith('src/')) {
    return { id: shown, owner: 'own' };
  }
  return { id: shown, owner: null };
}

// moduleList builds the module list of a written bundle: every JS chunk with all its modules
// and every CSS file with the CSS modules of the chunks that import it.
export function moduleList(bundle, webDir) {
  const chunks = [];
  const cssOwners = new Map();
  const unclassified = [];
  const take = (ids) => {
    const out = [];
    for (const id of ids) {
      const m = classify(id, webDir);
      if (m.owner === null) {
        unclassified.push(id);
      }
      out.push(m);
    }
    return out.sort((a, b) => byCodeUnit(a.id, b.id));
  };
  for (const item of Object.values(bundle)) {
    if (item.type !== 'chunk') {
      continue;
    }
    const ids = item.moduleIds ?? Object.keys(item.modules ?? {});
    chunks.push({ file: item.fileName, type: 'js', modules: take(ids) });
    const css = item.viteMetadata && item.viteMetadata.importedCss ? [...item.viteMetadata.importedCss] : [];
    for (const name of css) {
      const list = cssOwners.get(name) ?? new Set();
      for (const id of ids) {
        if (/\.css(\?|$)/.test(id)) {
          list.add(id);
        }
      }
      cssOwners.set(name, list);
    }
  }
  for (const item of Object.values(bundle)) {
    if (item.type === 'asset' && item.fileName.endsWith('.css')) {
      const ids = [...(cssOwners.get(item.fileName) ?? [])];
      if (ids.length === 0) {
        unclassified.push(`css file ${item.fileName} has no module`);
      }
      chunks.push({ file: item.fileName, type: 'css', modules: take(ids) });
    }
  }
  chunks.sort((a, b) => byCodeUnit(a.file, b.file));
  return { chunks, unclassified };
}

function readJson(file) {
  return JSON.parse(readFileSync(file, 'utf8'));
}

// licenseFiles returns the names of an installed package's license files, sorted.
export function licenseFiles(dir) {
  return readdirSync(dir)
    .filter((n) => LICENSE_FILE.test(n))
    .sort(byCodeUnit);
}

// noticeSet checks the module list against the lock and returns the final notice set:
// { packages, added, final, errors }.
export function noticeSet(chunks, lock, webDir) {
  const errors = [];
  const counts = new Map();
  for (const c of chunks) {
    for (const m of c.modules) {
      if (m.owner && m.owner.startsWith('node_modules/')) {
        counts.set(m.owner, (counts.get(m.owner) ?? 0) + 1);
      }
    }
  }
  const lockPkgs = lock.packages ?? {};
  const packages = [];
  for (const path of [...counts.keys()].sort(byCodeUnit)) {
    const entry = lockPkgs[path];
    if (!entry) {
      errors.push(`${path} is bundled but not in the lock`);
      continue;
    }
    packages.push({ path, name: path.slice(path.lastIndexOf('node_modules/') + 'node_modules/'.length), version: entry.version, modules: counts.get(path) });
  }
  const fixed = new Set(NOTICE_SET);
  const added = packages.map((p) => p.path).filter((p) => !fixed.has(p));
  const final = [...new Set([...NOTICE_SET, ...added])].sort(byCodeUnit);
  if (final.length === 0) {
    errors.push('the notice set is empty');
  }
  return { packages, added, final, errors };
}

// noticeFile writes the notice file of the final set. Each package section is headed by its
// lock path, name, lock version and license; each license file follows as a sub-section with
// its byte count and its bytes unchanged.
export function noticeFile(final, lock, webDir) {
  const errors = [];
  const parts = [
    Buffer.from(
      'Third-party software notices of the intuitik relation map prototype (web/prototype/dist).\n' +
        'Each package section holds the license files of the installed npm package byte for byte.\n' +
        'A file sub-section starts with a line "-------- FILE <name> bytes=<n> --------", is followed by\n' +
        'exactly <n> bytes of the file, a line feed and a line "-------- END FILE <name> --------".\n\n',
    ),
  ];
  const seen = new Set();
  for (const path of final) {
    if (seen.has(path)) {
      errors.push(`${path} has a duplicate section`);
      continue;
    }
    seen.add(path);
    const entry = (lock.packages ?? {})[path];
    if (!entry) {
      errors.push(`${path} is not in the lock`);
      continue;
    }
    const dir = join(webDir, path);
    let installed;
    let names;
    try {
      installed = readJson(join(dir, 'package.json'));
      names = licenseFiles(dir);
    } catch (err) {
      errors.push(`${path} cannot be read: ${err.message}`);
      continue;
    }
    if (installed.version !== entry.version) {
      errors.push(`${path} installed version ${installed.version} differs from the lock version ${entry.version}`);
      continue;
    }
    if (names.length === 0) {
      errors.push(`${path} has no license file`);
      continue;
    }
    const name = path.slice(path.lastIndexOf('node_modules/') + 'node_modules/'.length);
    parts.push(Buffer.from(`======== PACKAGE ${path} ========\nname: ${name}\nversion: ${entry.version}\nlicense: ${entry.license ?? '-'}\nfiles: ${names.length}\n`));
    for (const file of names) {
      let bytes;
      try {
        bytes = readFileSync(join(dir, file));
      } catch (err) {
        errors.push(`${path}/${file} cannot be read: ${err.message}`);
        continue;
      }
      parts.push(Buffer.from(`-------- FILE ${file} bytes=${bytes.length} --------\n`), bytes, Buffer.from(`\n-------- END FILE ${file} --------\n`));
    }
    parts.push(Buffer.from(`======== END PACKAGE ${path} ========\n\n`));
  }
  return { bytes: Buffer.concat(parts), errors };
}

// parseNoticeFile reads a notice file back into { path -> { name, version, license, files:
// [{ file, bytes }] } } and the errors of its form; it uses the byte counts, never searches
// the license texts.
export function parseNoticeFile(buf) {
  const sections = [];
  const errors = [];
  let pos = buf.indexOf('======== PACKAGE ');
  if (pos < 0) {
    errors.push('no package section');
    return { sections, errors };
  }
  const line = () => {
    const end = buf.indexOf(0x0a, pos);
    if (end < 0) {
      throw new Error(`no line end after byte ${pos}`);
    }
    const text = buf.subarray(pos, end).toString('utf8');
    pos = end + 1;
    return text;
  };
  try {
    while (pos < buf.length) {
      const head = line();
      if (head === '') {
        continue;
      }
      const m = /^======== PACKAGE (\S+) ========$/.exec(head);
      if (!m) {
        throw new Error(`unexpected line "${head}"`);
      }
      const s = { path: m[1], files: [] };
      const head2 = {};
      for (const key of ['name', 'version', 'license', 'files']) {
        const kv = /^(\w+): (.*)$/.exec(line());
        if (!kv || kv[1] !== key) {
          throw new Error(`${s.path}: expected ${key}`);
        }
        head2[key] = kv[2];
      }
      s.name = head2.name;
      s.version = head2.version;
      s.license = head2.license;
      const count = Number(head2.files);
      if (!Number.isInteger(count) || count < 0) {
        throw new Error(`${s.path}: bad file count "${head2.files}"`);
      }
      for (let i = 0; i < count; i++) {
        const fh = /^-------- FILE (\S+) bytes=(\d+) --------$/.exec(line());
        if (!fh) {
          throw new Error(`${s.path}: expected file ${i + 1}`);
        }
        const n = Number(fh[2]);
        const bytes = buf.subarray(pos, pos + n);
        if (bytes.length !== n) {
          throw new Error(`${s.path}/${fh[1]}: truncated`);
        }
        pos += n;
        if (buf[pos] !== 0x0a) {
          throw new Error(`${s.path}/${fh[1]}: no line feed after the bytes`);
        }
        pos += 1;
        if (line() !== `-------- END FILE ${fh[1]} --------`) {
          throw new Error(`${s.path}/${fh[1]}: no end line`);
        }
        s.files.push({ file: fh[1], bytes });
      }
      if (line() !== `======== END PACKAGE ${s.path} ========`) {
        throw new Error(`${s.path}: no end line`);
      }
      sections.push(s);
    }
  } catch (err) {
    errors.push(err.message);
  }
  return { sections, errors };
}

// modulesJson is the committed module list: 2-space JSON with one final line feed.
export function modulesJson(chunks, set) {
  const doc = {
    about: 'FA2-T8 E-NOTICE module list of web/prototype/dist: every final JS and CSS file with each module id (relative to web/) and its owner; the notice set is the fixed 30 non-dev lock paths plus packages found here outside them.',
    chunks,
    packages: set.packages,
    noticeSet: { fixed: [...NOTICE_SET].sort(byCodeUnit), addedFromModules: set.added, final: set.final },
  };
  return Buffer.from(`${JSON.stringify(doc, null, 2)}\n`);
}

export function gitBlob(bytes) {
  return createHash('sha1').update(Buffer.from(`blob ${bytes.length}\0`)).update(bytes).digest('hex');
}

export function sha256(bytes) {
  return createHash('sha256').update(bytes).digest('hex');
}

// notices is the Vite plugin. After the bundle is written it builds both files, compares them
// with the committed copies (or writes them when INTUITIK_NOTICES_WRITE=1), copies them into
// dist and prints one NOTICE-FILE line per dist file read back from disk.
export function notices({ webDir, protoDir }) {
  return {
    name: 'intuitik-prototype-notices',
    enforce: 'post',
    writeBundle(options, bundle) {
      const lock = readJson(join(webDir, 'package-lock.json'));
      const { chunks, unclassified } = moduleList(bundle, webDir);
      if (unclassified.length > 0) {
        throw new Error(`notices: unclassified modules: ${unclassified.join(', ')}`);
      }
      const set = noticeSet(chunks, lock, webDir);
      const notice = noticeFile(set.final, lock, webDir);
      const errors = [...set.errors, ...notice.errors];
      if (errors.length > 0) {
        throw new Error(`notices: ${errors.join('; ')}`);
      }
      const files = [
        [NOTICE_FILE, notice.bytes],
        [MODULES_FILE, modulesJson(chunks, set)],
      ];
      const committedDir = join(protoDir, 'notices');
      for (const [name, bytes] of files) {
        const committed = join(committedDir, name);
        if (process.env.INTUITIK_NOTICES_WRITE === '1') {
          writeFileSync(committed, bytes);
        } else if (!existsSync(committed) || !readFileSync(committed).equals(bytes)) {
          throw new Error(`notices: web/prototype/notices/${name} differs from the generated file (generated bytes=${bytes.length} sha256=${sha256(bytes)})`);
        }
        writeFileSync(join(options.dir, name), bytes);
      }
      for (const [name] of files) {
        const out = readFileSync(join(options.dir, name));
        const shown = relative(join(webDir, '..'), join(options.dir, name)).split(sep).join('/');
        console.log(`NOTICE-FILE path=${shown} bytes=${out.length} sha256=${sha256(out)} gitblob=${gitBlob(out)}`);
      }
      if (set.added.length > 0) {
        console.log(`NOTICE-SET added=${set.added.join(',')}`);
      }
      console.log(`NOTICE-SET final=${set.final.length} added=${set.added.length} chunks=${chunks.length}`);
    },
  };
}
