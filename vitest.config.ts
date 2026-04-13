import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    testTimeout: 60_000,
    hookTimeout: 30_000,
    retry: 1,
    include: ['e2e/**/*.test.ts'],
    fileParallelism: false,
    sequence: { concurrent: false },
  },
});
