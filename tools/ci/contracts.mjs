import SwaggerParser from "@apidevtools/swagger-parser";
import Ajv from "ajv";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const root = new URL("../../", import.meta.url);
const definition =
  process.argv[2] || new URL("backend/api/openapi.json", root).pathname;
const document = await SwaggerParser.validate(definition);
const resolved = await SwaggerParser.dereference(definition);
const fixtures = JSON.parse(
  readFileSync(new URL("backend/testdata/legacy/contracts.json", root)),
);
const ajv = new Ajv({ strict: false, validateFormats: false });
for (const [fixture, schema] of [
  ["created", "OrderEnvelope"],
  ["paymentPending", "PaymentStatus"],
  ["paymentPaid", "PaymentStatus"],
]) {
  const validate = ajv.compile(resolved.components.schemas[schema]);
  assert(
    validate(fixtures[fixture]),
    `${fixture} violates ${schema}: ${JSON.stringify(validate.errors)}`,
  );
}
assert.equal(document.openapi, "3.0.3");
for (const path of [
  "/",
  "/health/live",
  "/health/ready",
  "/api/orders",
  "/api/mpesa/callback",
]) {
  assert(document.paths[path], `missing documented route ${path}`);
}
console.log(
  "OpenAPI structure and legacy response fixtures validated; this does not enable legacy routes in Go.",
);
