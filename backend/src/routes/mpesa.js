import { Router } from "express";
import { timingSafeEqual } from "node:crypto";
import { prisma } from "../db.js";

const router = Router();

// --------------------
// POST /api/mpesa/callback
// Safaricom calls this URL after STK push completes (success or failure).
// Must be publicly reachable (use ngrok in development).
// --------------------
router.post("/callback", async (req, res) => {
  const expectedToken = process.env.MPESA_CALLBACK_SECRET;
  const providedToken = req.query.token;
  if (
    !expectedToken ||
    typeof providedToken !== "string" ||
    !safeTokenMatch(providedToken, expectedToken)
  ) {
    return res.status(401).json({ error: "Unauthorized callback." });
  }

  try {
    const body = req.body?.Body?.stkCallback;
    if (!body?.CheckoutRequestID || body.ResultCode === undefined) {
      return res.status(400).json({ error: "Invalid M-Pesa callback." });
    }

    const checkoutRequestId = body.CheckoutRequestID;
    const resultCode = String(body.ResultCode);
    const resultDesc = body.ResultDesc;

    // Find the matching transaction record
    const txn = await prisma.mpesaTransaction.findUnique({
      where: { checkoutRequestId },
    });
    if (!txn) {
      console.warn(
        "[Mpesa Callback] Unknown checkoutRequestId:",
        checkoutRequestId,
      );
      return res
        .status(503)
        .json({ error: "Transaction is not available yet." });
    }

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

    await prisma.$transaction([
      prisma.mpesaTransaction.update({
        where: { id: txn.id },
        data: {
          resultCode,
          resultDesc,
          mpesaReceiptNumber: receiptNumber,
          transactionDate,
          phoneNumber: phoneNumber ? String(phoneNumber) : null,
          status: stkStatus,
        },
      }),
      prisma.order.update({
        where: { id: txn.orderId },
        data: { paymentStatus },
      }),
    ]);

    console.log(
      `[Mpesa Callback] Order ${txn.orderId} → ${paymentStatus}` +
        (receiptNumber ? ` | Receipt: ${receiptNumber}` : ""),
    );

    return res.json({ ResultCode: 0, ResultDesc: "Accepted" });
  } catch (err) {
    console.error("[Mpesa Callback] Processing error:", err);
    return res.status(500).json({ error: "Callback processing failed." });
  }
});

function safeTokenMatch(providedToken, expectedToken) {
  const provided = Buffer.from(providedToken);
  const expected = Buffer.from(expectedToken);
  return (
    provided.length === expected.length &&
    timingSafeEqual(provided, expected)
  );
}

export default router;
