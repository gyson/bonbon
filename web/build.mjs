import { build } from 'esbuild';
import { Resvg } from '@resvg/resvg-js';
import { copyFile, readFile, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const output = new URL('../internal/webui/dist/', import.meta.url);
await rm(output, { recursive: true, force: true });
await build({
  entryPoints: [fileURLToPath(new URL('src/app.ts', import.meta.url))],
  bundle: true,
  format: 'esm',
  target: 'es2022',
  minify: true,
  loader: { '.svg': 'file' },
  assetNames: '[name]',
  outfile: fileURLToPath(new URL('assets/app.js', output)),
});
await copyFile(new URL('index.html', import.meta.url), new URL('index.html', output));
const logo = await readFile(new URL('src/bonbon.svg', import.meta.url), 'utf8');
// A square viewport preserves the horizontal logo's proportions in the 22-point
// macOS status item. Render at 2x; template coloring uses only the alpha channel.
const menuLogo = logo.replace('<svg ', '<svg width="44" height="44" ');
await writeFile(new URL('assets/menubar.png', output),
  new Resvg(menuLogo, { font: { loadSystemFonts: false } }).render().asPng());
const favicon = logo.replace('<svg ', '<svg width="32" height="32" ').replace('</svg>',
  '<style>:root{color:#232a30}@media(prefers-color-scheme:dark){:root{color:#f5f5f2}}</style></svg>');
await writeFile(new URL('assets/favicon.svg', output), favicon);
const licenses = await Promise.all(['@xterm/xterm', '@xterm/addon-fit'].map(async name =>
  `${name}\n\n${await readFile(new URL(`node_modules/${name}/LICENSE`, import.meta.url), 'utf8')}`));
await writeFile(new URL('assets/licenses.txt', output), licenses.join('\n\n'));
