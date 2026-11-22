// Entry of the relation map prototype (web/prototype/index.html, built only by
// web/prototype/vite.config.js). The design system's custom properties come from a CSS file
// generated at build time.
import 'virtual:design-system-tokens.css';
import './app.css';
import { createRoot } from 'react-dom/client';
import App from './App.jsx';
import { watchControls } from './controls.js';

const root = document.getElementById('prototype');
watchControls(root);
createRoot(root).render(<App />);
