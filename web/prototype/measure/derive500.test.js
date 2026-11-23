// The 500-Pod input of packet FA2-T8 6.4 is exactly the one Devgru computed: input sha256,
// Pod, node and bridge counts, byte count and output sha256.
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { describe, expect, it } from 'vitest';
import { canonical, derive500 } from './derive500.js';
import input from './input.json';
import { readTar } from './tar.js';

const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
const file = new URL('../../../test/e2e/prototype/testdata/l-graph.tar.gz', import.meta.url);

describe('derive500', () => {
  it('derives the 500-Pod input of perf-big byte for byte', () => {
    const gz = readFileSync(file);
    expect(sha(gz)).toBe(input.sha256);
    expect(gz.length).toBe(input.bytes);
    const member = readTar(gunzipSync(gz)).get(input.perfBig.member);
    expect(member.length).toBe(input.perfBig.bytes);
    expect(sha(member)).toBe(input.perfBig.sha256);
    const out = derive500(JSON.parse(Buffer.from(member).toString('utf8')));
    const bytes = Buffer.from(canonical(out), 'utf8');
    expect(out.nodes.filter((n) => n.kind === 'Pod').length).toBe(input.derived.pods);
    expect(out.nodes.length).toBe(input.derived.nodes);
    expect(out.bridges.length).toBe(input.derived.bridges);
    expect(bytes.length).toBe(input.derived.bytes);
    expect(sha(bytes)).toBe(input.derived.sha256);
  });
});
