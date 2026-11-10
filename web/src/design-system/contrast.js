// WCAG 2.x contrast of the token colour pairs used for text, for the gallery's token table.
import tokens from './tokens.js';

function channel(value) {
  const c = value / 255;
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

export function luminance(hex) {
  const n = hex.replace('#', '');
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(n.slice(i, i + 2), 16));
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

export function contrast(fg, bg) {
  const [a, b] = [luminance(fg), luminance(bg)].sort((x, y) => y - x);
  return (a + 0.05) / (b + 0.05);
}

// The text pairs of the token table: base text colours on the three backgrounds, each tone
// fg on its bg and on the surface, each node state fg on its bg.
export function textPairs(t = tokens) {
  const pairs = [];
  for (const fg of ['ink', 'sub', 'faint']) {
    for (const bg of ['surface', 'inset', 'canvas']) {
      pairs.push({ name: `${fg}/${bg}`, fg: t.color[fg], bg: t.color[bg] });
    }
  }
  for (const tone of ['broken', 'attention', 'optional', 'unknown']) {
    pairs.push({ name: `color.${tone} fg/bg`, fg: t.color[tone].fg, bg: t.color[tone].bg });
    pairs.push({ name: `color.${tone} fg/surface`, fg: t.color[tone].fg, bg: t.color.surface });
  }
  for (const state of ['failing', 'progressing', 'unknown', 'attention']) {
    pairs.push({ name: `nodeState.${state} fg/bg`, fg: t.nodeState[state].fg, bg: t.nodeState[state].bg });
  }
  return pairs.map((p) => ({ ...p, ratio: contrast(p.fg, p.bg) }));
}
