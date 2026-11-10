// The element gallery: the token table at the top (with the two values changed from the
// canvas next to their canvas values), then every item of fixtures.js, grouped by
// component id.
import { textPairs } from '../contrast.js';
import tokens from '../tokens.js';
import { GALLERY_ITEMS, GALLERY_TEXT } from './fixtures.js';
import { GalleryItem } from './registry.jsx';

const HEX = /^#[0-9a-fA-F]{6}$/;

function colourLeaves(node, path = [], out = []) {
  if (typeof node === 'string' && HEX.test(node)) {
    out.push({ name: path.join('.'), value: node });
  } else if (node && typeof node === 'object' && !Array.isArray(node)) {
    for (const [key, value] of Object.entries(node)) {
      if (!key.startsWith('_')) {
        colourLeaves(value, [...path, key], out);
      }
    }
  }
  return out;
}

const swatch = (value) => <span className="ds-swatch" data-colour={value.slice(1).toUpperCase()} aria-hidden="true" />;

function TokenTable() {
  return (
    <section className="gallery-tokens" id="tokens">
      <h2>{GALLERY_TEXT.tokens}</h2>
      <h3>{GALLERY_TEXT.changed}</h3>
      <table className="gallery-table" data-token-deviations="">
        <thead>
          <tr>
            <th>Token</th>
            <th>{GALLERY_TEXT.canvas}</th>
            <th>{GALLERY_TEXT.system}</th>
            <th>Why</th>
          </tr>
        </thead>
        <tbody>
          {tokens._deviation_from_canvas.map((d) => (
            <tr key={d.token} data-token={d.token}>
              <td>
                <code>{d.token}</code>
              </td>
              <td>
                {swatch(d.canvas)} <code data-canvas-value="">{d.canvas}</code>
              </td>
              <td>
                {swatch(d.system)} <code data-system-value="">{d.system}</code>
              </td>
              <td>{d.why}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <table className="gallery-table">
        <tbody>
          {colourLeaves(tokens).map((c) => (
            <tr key={c.name}>
              <td>
                <code>{c.name}</code>
              </td>
              <td>
                {swatch(c.value)} <code>{c.value}</code>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <h3>{GALLERY_TEXT.contrastTitle}</h3>
      <table className="gallery-table">
        <tbody>
          {textPairs().map((p) => (
            <tr key={p.name}>
              <td>
                <code>{p.name}</code>
              </td>
              <td>{p.ratio.toFixed(2)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h3>{GALLERY_TEXT.fonts}</h3>
      <p className="gallery-font gallery-font--sans">{GALLERY_TEXT.sansSample}</p>
      <p className="gallery-font gallery-font--mono">{GALLERY_TEXT.monoSample}</p>
    </section>
  );
}

export default function Gallery() {
  const groups = [];
  for (const item of GALLERY_ITEMS) {
    const last = groups[groups.length - 1];
    if (last && last.component === item.component) {
      last.items.push(item);
    } else {
      groups.push({ component: item.component, items: [item] });
    }
  }
  return (
    <main className="gallery">
      <h1>{GALLERY_TEXT.title}</h1>
      <p>{GALLERY_TEXT.intro}</p>
      <TokenTable />
      {groups.map((g) => (
        <section key={g.component} className="gallery-group" data-component={g.component}>
          <h2>{g.component}</h2>
          <div className="gallery-group__items">
            {g.items.map((item) => (
              <GalleryItem key={item.id} item={item} />
            ))}
          </div>
        </section>
      ))}
    </main>
  );
}
