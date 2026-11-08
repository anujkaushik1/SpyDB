// Loaded with `node --import` before the tests: lets Node's built-in test
// runner load the app's TypeScript the way Vite does (see resolve-ts.mjs).
// Node 22.18+ / 24 strips TypeScript types itself, so no extra packages.
import { register } from 'node:module'

register('./resolve-ts.mjs', import.meta.url)
