// T-12 card reason line (packet DS-1 section 6; specification DS-5, DS-E).
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { GALLERY_ITEMS } from '../gallery/fixtures.js';
import { GalleryItem } from '../gallery/registry.jsx';
import { RefRow, cardReason } from '../index.js';
import { decode, textOf, report, reportResults } from './helpers.js';

reportResults();

// The 9 reason sentences of prdr PRD/k8s-panel/UI.md section 4.4 (blob 84401ad9), each
// with its card sentence written out (\u2014 is the em dash).
const CASES = [
  ['Broken \u2014 selector app=web matches 0 Pods', 'selector app=web matches 0 Pods'],
  ['Broken \u2014 targetPort "http": no container port with this name', 'targetPort "http": no container port with this name'],
  ['Broken \u2014 ConfigMap payments-config not found', 'ConfigMap payments-config not found'],
  ['Broken \u2014 StorageClass "fast" not found (claim cannot bind)', 'StorageClass "fast" not found'],
  ['Broken \u2014 Service checkout not found', 'Service checkout not found'],
  ['Waiting \u2014 data-db-0 waits for consumer Pod db-0', 'data-db-0 waits for consumer Pod db-0'],
  ['Warning \u2014 targetPort 9000 is not declared by the container (traffic not verifiable)', 'targetPort 9000 is not declared by the container'],
  ['Unknown \u2014 no permission to read Secrets (the view role does not include Secrets)', 'no permission to read Secrets'],
  ['Optional \u2014 ConfigMap feature-flags not found', 'ConfigMap feature-flags not found'],
];

// True when `card` is `source` with only the leading state word and its dash and
// parenthesised explanations (with their leading space) removed, and nothing else.
function onlyRemoved(source, card) {
  const lead = source.match(/^[A-Z][a-z]+ \u2014 /);
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

// Texts of the elements that carry a full dictionary sentence (attribute data-full-reason).
function fullReasons(markup) {
  return [...markup.matchAll(/<(p|span)\s[^>]*data-full-reason=""[^>]*>([^<]*)<\/\1>/g)].map((m) => decode(m[2]));
}

// A problem unless one full-reason element holds exactly the sentence.
function fullSentenceProblems(markup, sentence) {
  return fullReasons(markup).includes(sentence) ? [] : [`no full-reason element with "${sentence}"`];
}

describe('T-12 card reason line', () => {
  it('T-12 cardReason removes only the leading state word with its dash and the parenthesised explanations, for all 9 sentences of UI.md 4.4', () => {
    expect(CASES).toHaveLength(9);
    for (const [sentence, expected] of CASES) {
      const card = cardReason(sentence);
      report(`T-12 "${sentence}" -> "${card}"`);
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
    expect(card).not.toContain('Broken \u2014');

    const sentences = CASES.map((c) => c[0]);
    // Inspector: every C-23 gallery item with a reason shows that full UI.md 4.4 sentence.
    const inspectorItems = GALLERY_ITEMS.filter((i) => i.component === 'C-23' && i.props.reason);
    expect(inspectorItems.length).toBeGreaterThanOrEqual(2);
    for (const item of inspectorItems) {
      expect(sentences).toContain(item.props.reason);
      const markup = renderToStaticMarkup(<GalleryItem item={item} />);
      report(`T-12 Inspector ${item.id} full reasons ${JSON.stringify(fullReasons(markup))}`);
      expect(fullSentenceProblems(markup, item.props.reason)).toEqual([]);
    }
    // Needs attention: the open groups' items show their full sentences.
    const attention = renderToStaticMarkup(<GalleryItem item={GALLERY_ITEMS.find((i) => i.id === 'C-22.groups')} />);
    report(`T-12 Needs attention C-22.groups full reasons ${JSON.stringify(fullReasons(attention))}`);
    for (const s of [CASES[0][0], CASES[2][0], CASES[6][0]]) {
      expect(fullSentenceProblems(attention, s)).toEqual([]);
    }
    // Negative fixtures: the cut sentence in the full-reason element, and no element at all.
    const cutMarkup = `<p data-full-reason="">${cardReason(CASES[6][0])}</p>`;
    expect(fullSentenceProblems(cutMarkup, CASES[6][0])).not.toEqual([]);
    expect(fullSentenceProblems(`<p>${CASES[6][0]}</p>`, CASES[6][0])).not.toEqual([]);
  });
});
