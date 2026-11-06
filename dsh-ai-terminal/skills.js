// skills.js — Harness skill provider of the AI terminal (plan Task 10).
//
// A skill is a plain <root>/<name>/SKILL.md (or <root>/<name>.md) file with the
// same frontmatter dsh-skill-filesystem and "npx skills add" expect, so the
// repository skills/trading-* tree is the single source for both surfaces. The
// provider also serves generated patterns from <dataDir>/skills (plan Task 19).
//
// Provider contract (verified against the installed Harness,
// docs/ai-terminal/dsh-integration.md §2.3): ctx.skills.registerProvider(create)
// where create(control) returns { name, list(options), get(candidate, options) }.

import fs from 'node:fs';
import path from 'node:path';

/** Provider name visible in the Harness catalog. */
export const SKILL_PROVIDER = 'ai-terminal';

/** Custom skill root band (same order as the filesystem provider's custom roots). */
const SKILL_RANK = 300;

/** Harness skill-name grammar. */
const SKILL_NAME = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

/** Strip one pair of matching quotes and undo the double-quote escapes. */
function unquote(value) {
  if (value.length >= 2 && value.startsWith('"') && value.endsWith('"')) {
    return value.slice(1, -1).replace(/\\(["\\])/g, '$1');
  }
  if (value.length >= 2 && value.startsWith("'") && value.endsWith("'")) {
    return value.slice(1, -1).replace(/''/g, "'");
  }
  return value;
}

/**
 * Pattern status of a generated SKILL.md (plan Task 19): repository skills
 * carry no metadata.status and are always visible, while generated patterns are
 * visible to the agent only after promotion to validated. Drafts and retired
 * patterns stay in the on-disk catalog but never reach the agent.
 */
function skillVisible(data) {
  const status = String(data.status === undefined ? '' : data.status).trim().toLowerCase();
  return status === '' || status === 'validated';
}

/**
 * Parse the YAML frontmatter subset used by SKILL.md files: flat scalar keys
 * between the leading --- lines. Returns { data, content } or null when the
 * frontmatter is missing.
 */
export function parseSkillFile(text) {
  if (typeof text !== 'string') return null;
  const normalized = text.replace(/\r\n?/g, '\n');
  if (!normalized.startsWith('---\n')) return null;
  const end = normalized.indexOf('\n---', 4);
  if (end < 0) return null;
  const header = normalized.slice(4, end);
  const bodyStart = normalized.indexOf('\n', end + 4);
  const content = bodyStart < 0 ? '' : normalized.slice(bodyStart + 1).replace(/^\n+/, '');
  const data = {};
  for (const line of header.split('\n')) {
    if (line.trim() === '' || line.startsWith('#')) continue;
    const colon = line.indexOf(':');
    if (colon <= 0) continue;
    const key = line.slice(0, colon).trim();
    const raw = line.slice(colon + 1).trim();
    if (key === '') continue;
    data[key] = unquote(raw);
  }
  return { data, content };
}

/** Parse one boolean frontmatter flag with the documented spellings. */
function frontmatterBoolean(value, fallback) {
  if (value === undefined) return fallback;
  const normalized = String(value).trim().toLowerCase();
  if (['true', 'yes', 'on', '1'].includes(normalized)) return true;
  if (['false', 'no', 'off', '0'].includes(normalized)) return false;
  return fallback;
}

/** Build one candidate from its parsed SKILL.md. */
function skillCandidate(file, directory, data) {
  const invocation = {
    modelInvocable: frontmatterBoolean(data['disable-model-invocation'], false) !== true,
    userInvocable: frontmatterBoolean(data['user-invocable'], true) !== false,
  };
  return {
    name: String(data.name),
    description: String(data.description),
    ...(data.whenToUse ? { whenToUse: String(data.whenToUse) } : {}),
    invocation,
    provider: SKILL_PROVIDER,
    source: 'ai-terminal',
    rank: SKILL_RANK,
    locator: file,
    path: file,
    resourceBase: { kind: 'directory', path: directory },
  };
}

/** List the skill files of one root: <name>/SKILL.md and flat <name>.md. */
function skillFiles(root) {
  const files = [];
  let entries;
  try {
    entries = fs.readdirSync(root, { withFileTypes: true });
  } catch {
    return files;
  }
  for (const entry of entries) {
    if (entry.isDirectory()) {
      const file = path.join(root, entry.name, 'SKILL.md');
      if (fs.existsSync(file)) files.push(file);
    } else if (entry.isFile() && entry.name.endsWith('.md')) {
      files.push(path.join(root, entry.name));
    }
  }
  return files.sort();
}

/** Read and validate one skill file into a candidate, or null. */
function readCandidate(file) {
  let text;
  try {
    text = fs.readFileSync(file, 'utf8');
  } catch {
    return null;
  }
  const parsed = parseSkillFile(text);
  if (!parsed) return null;
  const name = parsed.data.name;
  const description = parsed.data.description;
  if (typeof name !== 'string' || !SKILL_NAME.test(name)) return null;
  if (typeof description !== 'string' || description.trim() === '') return null;
  if (!skillVisible(parsed.data)) return null;
  return skillCandidate(file, path.dirname(file), parsed.data);
}

/**
 * Create the ai-terminal skill provider.
 *
 * options: { roots: string[], log? }
 * roots — directories scanned for skills (repo skills/ and <dataDir>/skills).
 */
export function createSkillProvider(options = {}) {
  const roots = (Array.isArray(options.roots) ? options.roots : []).filter((root) => typeof root === 'string' && root !== '');
  const log = typeof options.log === 'function' ? options.log : () => {};

  async function list() {
    const candidates = [];
    const seen = new Set();
    for (const root of roots) {
      for (const file of skillFiles(root)) {
        const candidate = readCandidate(file);
        if (candidate === null) continue;
        if (seen.has(candidate.name)) continue;
        seen.add(candidate.name);
        candidates.push(candidate);
      }
    }
    return candidates;
  }

  async function get(candidate) {
    const file = candidate && typeof candidate.locator === 'string' ? candidate.locator : '';
    if (file === '') return undefined;
    let text;
    try {
      text = fs.readFileSync(file, 'utf8');
    } catch {
      return undefined;
    }
    const parsed = parseSkillFile(text);
    if (!parsed) return undefined;
    const name = parsed.data.name;
    const description = parsed.data.description;
    if (typeof name !== 'string' || !SKILL_NAME.test(name)) return undefined;
    if (typeof description !== 'string' || description.trim() === '') return undefined;
    if (!skillVisible(parsed.data)) return undefined;
    return {
      name,
      description,
      ...(parsed.data.whenToUse ? { whenToUse: String(parsed.data.whenToUse) } : {}),
      invocation: {
        modelInvocable: frontmatterBoolean(parsed.data['disable-model-invocation'], false) !== true,
        userInvocable: frontmatterBoolean(parsed.data['user-invocable'], true) !== false,
      },
      source: 'ai-terminal',
      provider: SKILL_PROVIDER,
      resourceBase: { kind: 'directory', path: path.dirname(file) },
      path: file,
      content: parsed.content,
    };
  }

  log('debug', 'ai-terminal skills provider roots: ' + roots.join(', '));
  return { name: SKILL_PROVIDER, list, get };
}
