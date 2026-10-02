import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
// The typeface, from the console's own files: the latin subset in the three
// weights the page uses.
import '@fontsource/jetbrains-mono/latin-400.css';
import '@fontsource/jetbrains-mono/latin-500.css';
import '@fontsource/jetbrains-mono/latin-700.css';
import './index.css';

const root = document.getElementById('root');
if (!root) throw new Error('index.html has no #root element');

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
