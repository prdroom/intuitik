// T-8 component logic without a DOM (packet DS-1 section 6): handler and state functions
// only; the wired behaviour in a page is B-1.
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { createCopyHandler } from '../copy.js';
import { Button, Icon, NamespaceTile, StatePill, StatusTab } from '../index.js';

// Calls the element's click handler `times` times.
function clicksSeen(element, times) {
  for (let i = 0; i < times; i += 1) {
    element.props.onClick({ preventDefault() {} });
  }
}

// True when three clicks of the element made by `make(onClick)` run onClick three times.
function firesOncePerClick(make) {
  let calls = 0;
  clicksSeen(
    make(() => {
      calls += 1;
    }),
    3,
  );
  return calls === 3;
}

describe('T-8 component logic', () => {
  it('T-8 StatePill and StatusTab render nothing for Connected and Healthy', () => {
    expect(renderToStaticMarkup(<StatePill kind="link" state="connected" text="x" />)).toBe('');
    expect(renderToStaticMarkup(<StatePill kind="node" state="healthy" text="x" />)).toBe('');
    expect(renderToStaticMarkup(<StatusTab state="healthy" text="x" />)).toBe('');
    // Negative fixture: an anomalous state renders.
    expect(renderToStaticMarkup(<StatePill kind="link" state="broken" text="x" />)).not.toBe('');
  });

  it('T-8 Button calls its handler once per click and not when disabled, and sets aria-disabled', () => {
    expect(firesOncePerClick((onClick) => Button({ onClick, children: 'Open' }))).toBe(true);
    expect(Button({ onClick() {}, children: 'Open' }).props['aria-disabled']).toBeUndefined();
    let disabledCalls = 0;
    const disabled = Button({ onClick: () => (disabledCalls += 1), disabled: true, children: 'Open' });
    clicksSeen(disabled, 2);
    expect(disabledCalls).toBe(0);
    expect(disabled.props['aria-disabled']).toBe('true');
    expect(renderToStaticMarkup(disabled)).toContain('aria-disabled="true"');
    // Negative fixture: an element whose click runs the handler twice fails the check.
    expect(
      firesOncePerClick((onClick) => ({
        props: {
          onClick: (e) => {
            onClick(e);
            onClick(e);
          },
        },
      })),
    ).toBe(false);
  });

  it('T-8 Copy calls the injected clipboard function with the exact text, spaces kept; a refusal is a failure state, not a throw', async () => {
    const text = '  kubectl get pods -n shop  ';
    const seen = [];
    const results = [];
    const copy = createCopyHandler({ text, writeText: async (t) => seen.push(t), onResult: (r) => results.push(r) });
    await expect(copy()).resolves.toBe('copied');
    expect(seen).toEqual([text]);
    expect(seen[0].length).toBe(text.length);
    const refused = createCopyHandler({ text, writeText: () => Promise.reject(new Error('NotAllowedError')), onResult: (r) => results.push(r) });
    await expect(refused()).resolves.toBe('failed');
    const throwing = createCopyHandler({ text, writeText: () => { throw new Error('no clipboard'); }, onResult: (r) => results.push(r) });
    await expect(throwing()).resolves.toBe('failed');
    expect(results).toEqual(['copied', 'failed', 'failed']);
    // Negative fixture: a trimmed text is not the exact text.
    expect(text.trim()).not.toBe(seen[0]);
  });

  it('T-8 Icon with an unknown kind name renders the generic box without throwing', () => {
    const markup = renderToStaticMarkup(<Icon kind="FlowSchema" />);
    expect(markup).toContain('data-icon="generic"');
    expect(markup).toContain('aria-hidden="true"');
    expect(renderToStaticMarkup(<Icon kind="Pod" />)).toContain('data-icon="Pod"');
    // Negative fixture: a known kind is not the generic box.
    expect(renderToStaticMarkup(<Icon kind="Service" />)).not.toContain('data-icon="generic"');
  });

  it('T-8 NamespaceTile shows the input numbers unchanged (no summing)', () => {
    const markup = renderToStaticMarkup(
      <NamespaceTile name="shop" workloads={7} pods={{ ok: 9, progressing: 1, failing: 3 }} anomalies={{ broken: 2, failing: 3, warning: 1, waiting: 4, unknown: 5, optional: 6 }} labels={{}} />,
    );
    const numbers = [...markup.matchAll(/class="ds-tile__number">(\d+)</g)].map((m) => Number(m[1]));
    expect(numbers).toEqual([7, 9, 1, 3]);
    const summed = (n) => n.includes(13) || n.includes(20);
    expect(summed(numbers)).toBe(false);
    for (const value of ['2', '4', '5', '6']) {
      expect(markup).toMatch(new RegExp(`>${value}( |<)`));
    }
    // Negative fixture: a summed Pod count is found.
    expect(summed([7, 13])).toBe(true);
  });
});
