import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App.jsx';
import { startAuth } from './auth/index.jsx';

const root = createRoot(document.getElementById('root'));
startAuth((screen) => root.render(screen)).then((auth) => {
  if (auth.session) {
    root.render(
      <StrictMode>
        <App />
      </StrictMode>,
    );
  } else {
    root.render(auth.screen);
  }
});
