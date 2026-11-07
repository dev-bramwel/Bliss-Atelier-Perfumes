#!/usr/bin/env node

import { execFileSync } from 'node:child_process';

const dryRun = process.argv.includes('--dry-run');

console.log(`Starting issue dependency sync${dryRun ? ' (DRY RUN)' : ''}...`);

// 1. Fetch all repository issues
const rawIssues = execFileSync(
  'gh',
  ['issue', 'list', '--limit', '100', '--state', 'all', '--json', 'number,title,body'],
  { encoding: 'utf-8' }
);
const issues = JSON.parse(rawIssues);

const urlRegex = /issues\/(\d+)/g;
const depLineRegex = /Dependencies:\s*([^\n\r]+)/;
const blockedByTextRegex = /[Bb]locked by\s+#?(\d+)/g;

let updatedCount = 0;
let totalDepsCount = 0;

for (const iss of issues.sort((a, b) => a.number - b.number)) {
  const num = iss.number;
  const body = iss.body || '';

  const targetBlockers = new Set();

  // Extract from Dependencies: line
  const depMatch = body.match(depLineRegex);
  if (depMatch) {
    const depStr = depMatch[1];
    let match;
    while ((match = urlRegex.exec(depStr)) !== null) {
      const depNum = parseInt(match[1], 10);
      if (depNum !== num) {
        targetBlockers.add(depNum);
      }
    }
  }

  // Extract from "Blocked by #..."
  let blockMatch;
  while ((blockMatch = blockedByTextRegex.exec(body)) !== null) {
    const depNum = parseInt(blockMatch[1], 10);
    if (depNum !== num) {
      targetBlockers.add(depNum);
    }
  }

  if (targetBlockers.size === 0) {
    continue;
  }

  totalDepsCount += targetBlockers.size;

  // Check current blockedBy relationships
  const rawView = execFileSync(
    'gh',
    ['issue', 'view', String(num), '--json', 'blockedBy'],
    { encoding: 'utf-8' }
  );
  const currentBlockedBy = JSON.parse(rawView).blockedBy?.nodes || [];
  const currentBlockerNums = new Set(currentBlockedBy.map((n) => n.number));

  const toAdd = [...targetBlockers].filter((n) => !currentBlockerNums.has(n));

  if (toAdd.length > 0) {
    console.log(`Issue #${num} (${iss.title.slice(0, 35)}...): adding blockers: [${toAdd.join(', ')}]`);
    if (!dryRun) {
      const args = ['issue', 'edit', String(num)];
      for (const blocker of toAdd) {
        args.push('--add-blocked-by', String(blocker));
      }
      execFileSync('gh', args, { encoding: 'utf-8' });
      updatedCount++;
    }
  } else {
    console.log(`Issue #${num} is already up to date with blockers: [${[...targetBlockers].join(', ')}]`);
  }
}

console.log(`\nSync finished. Checked ${issues.length} issues.`);
console.log(`Total active dependencies defined: ${totalDepsCount}`);
console.log(`Issues updated: ${updatedCount}`);
