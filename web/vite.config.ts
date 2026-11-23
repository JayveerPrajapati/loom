import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:5678',
      '/hook': 'http://localhost:5678',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('@xyflow')) {
              return 'vendor-xyflow';
            }
            if (id.includes('react') || id.includes('scheduler')) {
              return 'vendor-react';
            }
            return undefined;
          }
          // The asset kit is dependency-free (type-only imports), so it can
          // live in its own cacheable chunk without a circular chunk edge.
          // ConfirmActionDialog is excluded because it imports the dialog hook.
          if (
            id.includes('/src/components/assets/') &&
            !id.includes('/ConfirmActionDialog.tsx')
          ) {
            return 'app-asset-kit';
          }
          return undefined;
        },
      },
    },
  },
});
