// T-12 card reason line (packet DS-1 section 6; specification DS-5, DS-E).
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { GALLERY_ITEMS } from '../gallery/fixtures.js';
import { GalleryItem } from '../gallery/registry.jsx';
import { RefRow, cardReason } from '../index.js';
import { textOf } from './helpers.js';

// The 9 reason sentences of prdr PRD/k8s-panel/UI.md section 4.4 (blob 84401ad9), each
// with its card sentence written out (— is the em dash).
const CASES = [
  ['Broken — selector app=web matches 0 Pods', 'selector app=web matches 0 Pods'],
  ['Broken — targetPort "http": no container port with this name', 'targetPort "http": no container port with this name'],
  ['Broken — ConfigMap payments-config not found', 'ConfigMap payments-config not found'],
  ['Broken — StorageClass "fast" not found (claim cannot bind)', 'StorageClass "fast" not found'],
  ['Broken — Service checkout not found', 'Service checkout not found'],
  ['Waiting — data-db-0 waits for consumer Pod db-0', 'data-db-0 waits for consumer Pod db-0'],
  ['Warning — targetPort 9000 is not declared by the container (traffic not verifiable)', 'targetPort 9000 is not declared by the container'],
  ['Unknown — no permission to read Secrets (the view role does not include Secrets)', 'no permission to read Secrets'],
  ['Optional — ConfigMap feature-flags not found', 'ConfigMap feature-flags not found'],
];

// True when `card` is `source` with only the leading state word and its dash and
// parenthesised explanations (with their leading space) removed, and nothing else.
function onlyRemoved(source, card) {
  const lead = source.match(/^[A-Z][a-z]+ — /);
  let rest = lead ? source.slice(lead[0].length) : source;
  const parts = [];
  let i = 0;
  for (const m of rest.matchAll(/ ?\([^()]*\)/g)) {
    parts.push(rest.slice(i, m.index));
    i = m.index + m[0].length;
  }
  parts.push(rest.slice(i));
  rest = parts.join('');
  return rest === card;
}

describe('T-12 card reason line', () => {
  it('T-12 cardReason removes only the leading state word with its dash and the parenthesised explanations, for all 9 sentences of UI.md 4.4', () => {
    expect(CASES).toHaveLength(9);
    for (const [sentence, expected] of CASES) {
      const card = cardReason(sentence);
      console.log(`T-12 "${sentence}" -> "${card}"`);
      expect(card).toBe(expected);
      expect(onlyRemoved(sentence, card)).toBe(true);
    }
    // Negative fixture: a card sentence with a changed word is a new string.
    expect(onlyRemoved(CASES[0][0], 'selector app=web matches no Pods')).toBe(false);
    expect(onlyRemoved(CASES[6][0], 'targetPort 9000 is not declared')).toBe(false);
  });

  it('T-12 the card renders the cut sentence; the Inspector and Needs attention items get the full sentence', () => {
    const [sentence, expected] = CASES[3];
    const card = textOf(renderToStaticMarkup(<RefRow id="sc" state="broken" name="class" value="fast" reason={sentence} />));
    expect(card).toContain(expected);
    expect(card).not.toContain('(claim cannot bind)');
    expect(card).not.toContain('Broken —');
    const attention = GALLERY_ITEMS.find((i) => i.id === 'C-22.groups');
    const markup = textOf(renderToStaticMarkup(<GalleryItem item={attention} />));
    expect(markup).toContain(CASES[6][0]);
  });
});
