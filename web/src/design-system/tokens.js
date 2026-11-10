// The design system tokens (bytes of prdr tokens.json, packet DS-1 section 3). Components
// read values from here; colours reach the page only through the generated CSS custom
// properties.
import tokens from './tokens.json';

export default tokens;

export const LINK_STATES = ['connected', 'broken', 'waiting', 'warning', 'unknown', 'optional'];
export const NODE_STATES = ['healthy', 'progressing', 'failing', 'unknown'];
export const TONES = ['broken', 'attention', 'optional', 'unknown'];

// Glyph of a link state or a node state; null for Connected and Healthy.
export function linkGlyph(state) {
  return tokens.link[state]?.glyph ?? null;
}

export function nodeGlyph(state) {
  return tokens.nodeState[state]?.glyph ?? null;
}
