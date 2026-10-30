import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { ReactFlowProvider } from '@xyflow/react';
import App from './App';
import './styles.css';
import './ui-pages.css';

const container = document.getElementById('root');
if (!container) throw new Error('missing #root');

createRoot(container).render(
  <StrictMode>
    <ReactFlowProvider>
      <App />
    </ReactFlowProvider>
  </StrictMode>,
);
