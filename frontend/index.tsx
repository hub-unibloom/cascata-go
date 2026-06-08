import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import './index.css';

// Intercepta o window.fetch para injetar automaticamente o branch na URL das requisições
// A fonte da verdade é sempre a URL do navegador (window.location.hash)
const originalFetch = window.fetch;
window.fetch = async function(...args) {
  let [resource, config] = args;
  
  if (typeof resource === 'string' && (resource.startsWith('/api/data/') || resource.startsWith('/rest/v1/'))) {
    // Tenta extrair o branch da URL atual
    // Padrão esperado na URL: #/project/id/secao/branch/nome-da-branch
    const hash = window.location.hash;
    const branchMatch = hash.match(/\/branch\/([^/?]+)/);
    
    if (branchMatch && branchMatch[1] && branchMatch[1] !== 'live') {
      const branchName = branchMatch[1];
      
      // Injeta /branch/nome-da-branch de forma inteligente (evitando duplicações)
      if (resource.startsWith('/api/data/') && !resource.includes('/branch/')) {
        const parts = resource.split('/');
        // Array resultante: ['', 'api', 'data', '{slug}', 'rest_of_path...']
        if (parts.length > 4) {
          const slug = parts[3];
          const rest = parts.slice(4).join('/');
          resource = `/api/data/${slug}/branch/${branchName}/${rest}`;
        }
      } else if (resource.startsWith('/rest/v1/') && !resource.includes('/branch/')) {
        resource = `/branch/${branchName}${resource}`;
      }
    }
  }
  
  return originalFetch.apply(this, [resource, config]);
};

const rootElement = document.getElementById('root');
if (!rootElement) {
  throw new Error("Could not find root element to mount to");
}

const root = ReactDOM.createRoot(rootElement);
root.render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);