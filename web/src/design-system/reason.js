// Card reason line (specification DS-5, DS-E): the card sentence is the dictionary sentence
// of UI.md 4.4 with the leading state word and its dash removed and every parenthesised
// explanation removed, and nothing else. The full sentence stays for the Inspector and the
// Needs attention items.
const LEADING_STATE = /^(Broken|Waiting|Warning|Unknown|Optional) \u2014 /;
const PARENTHESISED = / ?\([^()]*\)/g;

export function cardReason(sentence) {
  return sentence.replace(LEADING_STATE, '').replace(PARENTHESISED, '');
}
