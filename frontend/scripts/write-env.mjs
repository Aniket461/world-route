import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));

/** Netlify UI sometimes stores values as "=https://..." when pasted as KEY=VALUE. */
function normalizeApiBaseUrl(raw) {
  let v = String(raw || '').trim();
  // Strip accidental leading "=" from env paste mistakes.
  while (v.startsWith('=')) {
    v = v.slice(1).trim();
  }
  v = v.replace(/\/$/, '');
  if (v && !/^https?:\/\//i.test(v)) {
    console.warn(`[write-env] API_BASE_URL looks invalid (missing http/https): ${v}`);
  }
  return v;
}

const apiBaseUrl = normalizeApiBaseUrl(process.env.API_BASE_URL);

const contents = `export const environment = {
  production: true,
  apiBaseUrl: '${apiBaseUrl.replace(/\\/g, '\\\\').replace(/'/g, "\\'")}',
};
`;

const target = join(__dirname, '..', 'src', 'environments', 'environment.production.ts');
writeFileSync(target, contents, 'utf8');
console.log(`[write-env] API_BASE_URL=${apiBaseUrl || '(empty — same-origin / Netlify /api proxy)'}`);
