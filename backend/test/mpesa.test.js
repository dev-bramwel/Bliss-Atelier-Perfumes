import test from "node:test";
import assert from "node:assert/strict";
import { formatDarajaReferences } from "../src/services/mpesa.js";

test("keeps Daraja account reference and description within provider limits", () => {
  const references = formatDarajaReferences("cm1234567890123456789012345");

  assert.equal(references.accountReference, "456789012345");
  assert.ok(references.accountReference.length <= 12);
  assert.equal(references.transactionDescription, "Bliss order");
  assert.ok(references.transactionDescription.length <= 13);
});