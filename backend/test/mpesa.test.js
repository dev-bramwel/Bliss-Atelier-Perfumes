import test from "node:test";
import assert from "node:assert/strict";
import {
  createCallbackSignature,
  formatDarajaReferences,
  verifyCallbackSignature,
} from "../src/services/mpesa.js";

test("keeps Daraja account reference and description within provider limits", () => {
  const references = formatDarajaReferences("cm1234567890123456789012345");

  assert.equal(references.accountReference, "456789012345");
  assert.ok(references.accountReference.length <= 12);
  assert.equal(references.transactionDescription, "Bliss order");
  assert.ok(references.transactionDescription.length <= 13);
});

test("signs order IDs for recoverable M-Pesa callbacks", () => {
  const signature = createCallbackSignature("order-123", "test-secret");

  assert.equal(signature.length, 64);
  assert.equal(
    verifyCallbackSignature("order-123", signature, "test-secret"),
    true,
  );
  assert.equal(
    verifyCallbackSignature("order-456", signature, "test-secret"),
    false,
  );
  assert.equal(
    verifyCallbackSignature("order-123", "not-a-signature", "test-secret"),
    false,
  );
});