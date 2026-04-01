import fs from 'node:fs'
import path from 'node:path'
import { defineConfig } from 'vite'

const apiPort = parseInt(process.env.API_PORT || '0') || (parseInt(process.env.PORT || '5173') + 1);

const certDir = path.resolve(__dirname, '../.certs');
const certFile = path.join(certDir, 'localhost.pem');
const keyFile = path.join(certDir, 'localhost-key.pem');
const hasCerts = fs.existsSync(certFile) && fs.existsSync(keyFile);

export default defineConfig({
  server: {
    port: parseInt(process.env.PORT || '5173'),
    strictPort: true,
    ...(hasCerts && {
      https: {
        cert: fs.readFileSync(certFile),
        key: fs.readFileSync(keyFile),
      },
    }),
    proxy: {
      '/api': {
        target: `http://localhost:${apiPort}`,
        changeOrigin: true,
      },
    },
  },
})
