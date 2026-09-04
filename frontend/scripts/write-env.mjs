import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const apiBaseUrl = (process.env.API_BASE_URL || '').replace(/\/$/, '');

const contents = `export const environment = {
  production: true,
  apiBaseUrl: '${apiBaseUrl.replace(/'/g, "\\'")}',
};
`;

const target = join(__dirname, '..', 'src', 'environments', 'environment.production.ts');
writeFileSync(target, contents, 'utf8');
console.log(`[write-env] API_BASE_URL=${apiBaseUrl || '(empty — same-origin)'}`);
