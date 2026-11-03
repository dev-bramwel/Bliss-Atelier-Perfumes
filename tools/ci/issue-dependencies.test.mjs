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

test('PR closing keyword regex extracts target issue numbers correctly', () => {
  const closingRegex = /(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)\s+#(\d+)/gi;
  const prBody = `
    ## Summary
    This PR closes #16 and fixes #15.
    Also resolved #20.
  `;

  const issues = [];
  let m;
  while ((m = closingRegex.exec(prBody)) !== null) {
    issues.push(parseInt(m[1], 10));
  }
  assert.deepEqual(issues, [16, 15, 20]);
});

test('self-dependencies are ignored', () => {
  const issueNum = 16;
  const rawDeps = [15, 16, 17];
  const filtered = rawDeps.filter((d) => d !== issueNum);
  assert.deepEqual(filtered, [15, 17]);
});
