# vanilla-jsoneditor 3.13.0 (standalone bundle)

The JSON editor used by `JsonEditorModal.tsx`, kept here instead of in
`node_modules`: the npm package pulls in 63 packages, while its
`standalone.js` already contains everything and imports nothing.

- Source: npm package `vanilla-jsoneditor@3.13.0` (published 2026-07-24),
  integrity `sha512-u9C+D8Ocgr39Gnsfu7USjilfKssqbDqZehMdE0qbwIFnZFdNwkU4iKeEvJxvYbmlVi/sMLxMTrU14PhtXVyiZg==`
- Files: `standalone.js`, `standalone.d.ts`, `themes/jse-theme-dark.css`
  (as `jse-theme-dark.css`), `LICENSE.md` (ISC)

To update, pick a version at least 7 days old, then:

```sh
npm pack vanilla-jsoneditor@<version>   # in an empty folder
tar -xzf vanilla-jsoneditor-<version>.tgz
cp package/standalone.js package/standalone.d.ts package/themes/jse-theme-dark.css package/LICENSE.md frontend/src/vendor/vanilla-jsoneditor/
```
