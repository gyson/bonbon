import { execFileSync } from 'node:child_process';
import { readFile, readdir } from 'node:fs/promises';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const go = (...args) => execFileSync('go', args, { cwd: root, encoding: 'utf8' }).trim();

async function noticeFiles(directory) {
  const paths = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) paths.push(...await noticeFiles(path));
    else if (entry.isFile() && /^(licen[cs]e|notice|copying|copyright|unlicense)([.-].*)?$/i.test(entry.name)) {
      paths.push(path);
    }
  }
  return paths.sort();
}

// Embed notices so they stay with the executable after installation or updating.
export async function licenseNotices() {
  const notices = [];
  async function add(label, path) {
    notices.push(`${label}\n\n${await readFile(path, 'utf8')}`);
  }
  await add('BonBon — MIT License', join(root, 'LICENSE'));
  for (const name of ['@xterm/xterm', '@xterm/addon-fit']) {
    await add(name, join(root, 'web/node_modules', name, 'LICENSE'));
  }
  // Some packaged Go installations omit the license from GOROOT.
  await add(`Go standard library (${go('env', 'GOVERSION')})`, join(root, 'licenses/Go-LICENSE.txt'));

  go('mod', 'download');
  const modules = JSON.parse(go('mod', 'edit', '-json')).Require.map(module => module.Path);
  const records = go('list', '-m', '-f', '{{.Path}}@{{.Version}}{{"\t"}}{{.Dir}}', ...modules);
  for (const record of records.split('\n')) {
    const [name, directory] = record.split('\t');
    if (!directory) throw new Error(`Missing downloaded source for ${name}`);
    const paths = await noticeFiles(directory);
    if (paths.length === 0) throw new Error(`Missing license notices for ${name}`);
    for (const path of paths) await add(`${name} — ${relative(directory, path)}`, path);
  }
  // goffi's NOTICE attributes its fakecgo code to Apache-2.0, but its module
  // contains only the MIT license text. Include the attributed license as well.
  await add('github.com/go-webgpu/goffi/internal/fakecgo — Apache-2.0', join(root, 'licenses/Apache-2.0.txt'));
  return notices.join('\n\n' + '='.repeat(72) + '\n\n');
}
