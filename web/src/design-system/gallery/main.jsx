// Entry of the element gallery (web/gallery.html, built only by web/vite.gallery.config.js).
// The tokens' custom properties come from a CSS file generated at build time.
import 'virtual:design-system-tokens.css';
import './gallery.css';
import { createRoot } from 'react-dom/client';
import Gallery from './Gallery.jsx';

createRoot(document.getElementById('gallery')).render(<Gallery />);
