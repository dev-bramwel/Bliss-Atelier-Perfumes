import test from 'node:test';
import assert from 'node:assert/strict';

test('dependency extraction parses single and multiple issue URLs', () => {
  const urlRegex = /issues\/(\d+)/g;
  const depLineRegex = /Dependencies:\s*([^\n\r]+)/;

  const body1 = 'Target: week 2. Dependencies: [R09](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/15). Existing backlog: BAP-004.';
  const match1 = body1.match(depLineRegex);
  assert.ok(match1);

  const deps1 = [];
  let m;
  while ((m = urlRegex.exec(match1[1])) !== null) {
    deps1.push(parseInt(m[1], 10));
  }
  assert.deepEqual(deps1, [15]);

  const body2 = 'Dependencies: [R04](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/10),[R06](https://github.com/dev-bramwel/Bliss-Atelier-Perfumes/issues/12).';
  const match2 = body2.match(depLineRegex);
  assert.ok(match2);

  const deps2 = [];
  urlRegex.lastIndex = 0;
  while ((m = urlRegex.exec(match2[1])) !== null) {
    deps2.push(parseInt(m[1], 10));
  }
  assert.deepEqual(deps2, [10, 12]);
});

test('dependency extraction parses text-based Blocked by #num references', () => {
  const blockedByTextRegex = /[Bb]locked by\s+#?(\d+)/g;
  const body = 'This task is blocked by #15 and also Blocked by 18.';

  const deps = [];
  let m;
  while ((m = blockedByTextRegex.exec(body)) !== null) {
    deps.push(parseInt(m[1], 10));
  }
  assert.deepEqual(deps, [15, 18]);
});

test('PR closing keyword regex handles standard, colon-separated, and mixed whitespace', () => {
  const closingRegex = /(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)(?::\s*|\s+)#(\d+)/gi;

  const testCases = [
    { input: 'Closes #16', expected: [16] },
    { input: 'Closes: #16', expected: [16] },
    { input: 'Fixes #15', expected: [15] },
    { input: 'Fixes: #15', expected: [15] },
    { input: 'fixes:#15', expected: [15] },
    { input: 'Resolved: #20 and closes #21', expected: [20, 21] },
    { input: 'Fixed:   #30', expected: [30] },
    { input: 'Not a keyword: relates #12', expected: [] }
  ];

  for (const { input, expected } of testCases) {
    const issues = [];
    let m;
    const regex = new RegExp(closingRegex.source, closingRegex.flags);
    while ((m = regex.exec(input)) !== null) {
      issues.push(parseInt(m[1], 10));
    }
    assert.deepEqual(issues, expected, `Failed on input: "${input}"`);
  }
});

test('PR closing references scan both PR body and commit messages', () => {
  const closingRegex = /(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)(?::\s*|\s+)#(\d+)/gi;

  const prBody = '## Summary\nRefactors auth modules.\nCloses: #16';
  const commitMessages = [
    'feat(auth): initial commit',
    'fix(auth): resolve edge case\n\nFixes: #18',
    'chore: update docs'
  ];

  const textsToScan = [prBody, ...commitMessages];
  const targetIssueNums = new Set();

  for (const text of textsToScan) {
    let m;
    const regex = new RegExp(closingRegex.source, closingRegex.flags);
    while ((m = regex.exec(text)) !== null) {
      targetIssueNums.add(parseInt(m[1], 10));
    }
  }

  assert.deepEqual(Array.from(targetIssueNums).sort(), [16, 18]);
});

test('self-dependencies are ignored', () => {
  const issueNum = 16;
  const rawDeps = [15, 16, 17];
  const filtered = rawDeps.filter((d) => d !== issueNum);
  assert.deepEqual(filtered, [15, 17]);
});
