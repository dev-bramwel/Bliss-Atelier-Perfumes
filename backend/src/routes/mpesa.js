import { Router } from "express";
import { timingSafeEqual } from "node:crypto";
import { prisma } from "../db.js";
import { verifyCallbackSignature } from "../services/mpesa.js";

const router = Router();

// --------------------
// POST /api/mpesa/callback
// Safaricom calls this URL after STK push completes (success or failure).
// Must be publicly reachable (use ngrok in development).
// --------------------
router.post("/callback", async (req, res) => {
  const expectedToken = process.env.MPESA_CALLBACK_SECRET;
  const providedToken = req.query.token;
  const callbackOrderId = req.query.orderId;
  const hasSignedOrderId = verifyCallbackSignature(
    callbackOrderId,
    req.query.signature,
  );
  const hasLegacyToken =
    typeof providedToken === "string" &&
    safeTokenMatch(providedToken, expectedToken);
  if (!expectedToken || (!hasSignedOrderId && !hasLegacyToken)) {
    return res.status(401).json({ error: "Unauthorized callback." });
  }

  try {
    const body = req.body?.Body?.stkCallback;
    if (!body?.CheckoutRequestID || body.ResultCode === undefined) {
      return res.status(400).json({ error: "Invalid M-Pesa callback." });
    }

    const checkoutRequestId = body.CheckoutRequestID;
    const orderId = hasSignedOrderId
      ? callbackOrderId
      : (
          await prisma.mpesaTransaction.findUnique({
            where: { checkoutRequestId },
            select: { orderId: true },
          })
        )?.orderId;
    if (!orderId) {
      console.warn(
        "[Mpesa Callback] Cannot map checkoutRequestId:",
        checkoutRequestId,
      );
      return res
        .status(503)
        .json({ error: "Transaction is not available yet." });
    }

    const resultCode = String(body.ResultCode);
    const resultDesc = body.ResultDesc;

    let receiptNumber = null;
    let transactionDate = null;
    let phoneNumber = null;

    // ResultCode 0 = success; anything else = failure
    if (resultCode === "0") {
      const items = body.CallbackMetadata?.Item ?? [];
      receiptNumber =
        items.find((i) => i.Name === "MpesaReceiptNumber")?.Value ?? null;
      transactionDate = String(
        items.find((i) => i.Name === "TransactionDate")?.Value ?? "",
      );
      phoneNumber = String(
        items.find((i) => i.Name === "PhoneNumber")?.Value ?? "",
      );
    }

    const stkStatus = resultCode === "0" ? "SUCCESS" : "FAILED";
    const paymentStatus = resultCode === "0" ? "PAID" : "FAILED";

    await prisma.$transaction(async (transaction) => {
      await transaction.mpesaTransaction.upsert({
        where: { orderId },
        create: {
          orderId,
          merchantRequestId: body.MerchantRequestID ?? null,
          checkoutRequestId,
          resultCode,
          resultDesc,
          mpesaReceiptNumber: receiptNumber,
          transactionDate,
          phoneNumber: phoneNumber ? String(phoneNumber) : null,
          status: stkStatus,
        },
        update: {
          merchantRequestId: body.MerchantRequestID ?? null,
          checkoutRequestId,
          resultCode,
          resultDesc,
          mpesaReceiptNumber: receiptNumber,
          transactionDate,
          phoneNumber: phoneNumber ? String(phoneNumber) : null,
          status: stkStatus,
        },
      });
      await transaction.order.update({
        where: { id: orderId },
        data: { paymentStatus },
      });
    });

    console.log(
      `[Mpesa Callback] Order ${orderId} → ${paymentStatus}` +
        (receiptNumber ? ` | Receipt: ${receiptNumber}` : ""),
    );

    return res.json({ ResultCode: 0, ResultDesc: "Accepted" });
  } catch (err) {
    console.error("[Mpesa Callback] Processing error:", err);
    return res.status(500).json({ error: "Callback processing failed." });
  }
});

function safeTokenMatch(providedToken, expectedToken) {
  if (typeof expectedToken !== "string") return false;
  const provided = Buffer.from(providedToken);
  const expected = Buffer.from(expectedToken);
  return (
    provided.length === expected.length &&
    timingSafeEqual(provided, expected)
  );
}

export default router;
