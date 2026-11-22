// Reading the L graph file (packet FA2-T8 6.4): a gzip of a ustar archive of JSON files.
// readTar works on the bytes after gunzip, in the browser and in Node.

const decoder = new TextDecoder();

function field(bytes, offset, length) {
  const end = bytes.indexOf(0, offset);
  return decoder.decode(bytes.subarray(offset, end < 0 || end > offset + length ? offset + length : end));
}

// readTar returns a Map of member name -> bytes for every regular file of the archive.
export function readTar(bytes) {
  const out = new Map();
  let offset = 0;
  while (offset + 512 <= bytes.length) {
    const name = field(bytes, offset, 100);
    if (!name) {
      break;
    }
    const size = parseInt(field(bytes, offset + 124, 12).trim() || '0', 8);
    const type = String.fromCharCode(bytes[offset + 156] || 48);
    const prefix = field(bytes, offset + 345, 155);
    const full = (prefix ? `${prefix}/${name}` : name).replace(/^\.\//, '');
    if (type === '0' || type === '\0') {
      out.set(full, bytes.subarray(offset + 512, offset + 512 + size));
    }
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  return out;
}

export function hex(buffer) {
  return Array.from(new Uint8Array(buffer), (b) => b.toString(16).padStart(2, '0')).join('');
}

// Browser helpers: sha256 with Web Crypto and gunzip with DecompressionStream.
export async function sha256Hex(bytes) {
  return hex(await crypto.subtle.digest('SHA-256', bytes));
}

export async function gunzip(bytes) {
  const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream('gzip'));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}
