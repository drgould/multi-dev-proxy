import { defineConfig } from 'vite'

const apiPort = parseInt(process.env.API_PORT || '0') || (parseInt(process.env.PORT || '5173') + 1);

export default defineConfig({
  server: {
    port: parseInt(process.env.PORT || '5173'),
    strictPort: true,
    proxy: {
      '/api': {
        target: `http://localhost:${apiPort}`,
        changeOrigin: true,
      },
    },
  },
})
