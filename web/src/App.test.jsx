import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import App from './App.jsx';

describe('App', () => {
  it('renders the empty page root element', () => {
    const html = renderToStaticMarkup(<App />);
    expect(html).toBe('<main id="intuitik-app"></main>');
  });
});
