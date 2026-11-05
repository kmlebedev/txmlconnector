// Tests of the ai-terminal skill provider (plan Tasks 10/19/25): the same
// repository skills/ tree feeds DeepSeek Harness and npx installations, and the
// generated pattern drafts stay invisible to the agent until promotion.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import nodePath from 'node:path';
import { test } from 'node:test';

import { createSkillProvider, parseSkillFile } from '../skills.js';

// writeSkill creates <root>/<dir>/SKILL.md with the given frontmatter lines.
function writeSkill(root, dir, frontmatter, body = 'Тело навыка.') {
  const skillDir = nodePath.join(root, dir);
  fs.mkdirSync(skillDir, { recursive: true });
  const header = frontmatter.join('\n');
  fs.writeFileSync(nodePath.join(skillDir, 'SKILL.md'), '---\n' + header + '\n---\n\n# ' + dir + '\n\n' + body + '\n');
}

function tempRoot(t) {
  const root = fs.mkdtempSync(nodePath.join(os.tmpdir(), 'ai-terminal-skills-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  return root;
}

test('skill provider hides drafts and retired patterns from the agent', async (t) => {
  const root = tempRoot(t);
  const repoSkills = nodePath.join(root, 'repo-skills');
  const dataSkills = nodePath.join(root, 'data-skills');
  writeSkill(repoSkills, 'trading-terminal', ['name: trading-terminal', 'description: Use when the user asks for a quote.']);
  writeSkill(dataSkills, 'pattern-draft', ['name: pattern-draft', 'description: Use when the draft is not proven.', 'metadata:', '  status: draft']);
  writeSkill(dataSkills, 'pattern-validated', ['name: pattern-validated', 'description: Use when the pattern passed OOS.', 'metadata:', '  status: validated']);
  writeSkill(dataSkills, 'pattern-retired', ['name: pattern-retired', 'description: Use when the pattern was retired.', 'metadata:', '  status: retired']);
  const provider = createSkillProvider({ roots: [repoSkills, dataSkills] });
  const names = (await provider.list()).map((candidate) => candidate.name).sort();
  assert.deepEqual(names, ['pattern-validated', 'trading-terminal']);
  const validated = (await provider.list()).find((candidate) => candidate.name === 'pattern-validated');
  assert.match((await provider.get(validated)).content, /Тело навыка\./);
  const draftLocator = { locator: nodePath.join(dataSkills, 'pattern-draft', 'SKILL.md') };
  assert.equal(await provider.get(draftLocator), undefined);
  const retiredLocator = { locator: nodePath.join(dataSkills, 'pattern-retired', 'SKILL.md') };
  assert.equal(await provider.get(retiredLocator), undefined);
});

test('skill provider keeps repository skills without a status visible', async (t) => {
  const root = tempRoot(t);
  writeSkill(root, 'trading-pattern-author', ['name: trading-pattern-author', 'description: Use when drafting a pattern.']);
  const provider = createSkillProvider({ roots: [root] });
  const candidates = await provider.list();
  assert.equal(candidates.length, 1);
  assert.equal(candidates[0].name, 'trading-pattern-author');
});

test('frontmatter parser unescapes quoted pattern descriptions', () => {
  const text = '---\nname: pattern-quotes\ndescription: "Use when the agent sees \\"RSI\\" and a \\\\ path"\n---\n\n# pattern-quotes\n';
  const parsed = parseSkillFile(text);
  assert.ok(parsed);
  assert.equal(parsed.data.name, 'pattern-quotes');
  assert.equal(parsed.data.description, 'Use when the agent sees "RSI" and a \\ path');
});
