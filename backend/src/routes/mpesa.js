import { Router } from "express";
import { PrismaClient } from "../../generated/prisma/index.js";

const router = Router();
const prisma = new PrismaClient();

// --------------------
// POST /api/mpesa/callback
// Safaricom calls this URL after STK push completes (success or failure).
// Must be publicly reachable (use ngrok in development).
// --------------------
router.post("/callback", async (req, res) => {
  // Always ACK immediately so Safaricom doesn't retry
  res.json({ ResultCode: 0, ResultDesc: "Accepted" });

  try {
    const body     = req.body?.Body?.stkCallback;
    if (!body) return;

    const checkoutRequestId  = body.CheckoutRequestID;
    const merchantRequestId  = body.MerchantRequestID;
    const resultCode         = String(body.ResultCode);
    const resultDesc         = body.ResultDesc;

    // Find the matching transaction record
    const txn = await prisma.mpesaTransaction.findUnique({
      where: { checkoutRequestId },
    });
    if (!txn) {
      console.warn("[Mpesa Callback] Unknown checkoutRequestId:", checkoutRequestId);
      return;
    }

    let receiptNumber   = null;
    let transactionDate = null;
    let phoneNumber     = null;

    // ResultCode 0 = success; anything else = failure
    if (resultCode === "0") {
      const items = body.CallbackMetadata?.Item ?? [];
      receiptNumber   = items.find((i) => i.Name === "MpesaReceiptNumber")?.Value ?? null;
      transactionDate = String(items.find((i) => i.Name === "TransactionDate")?.Value ?? "");
      phoneNumber     = String(items.find((i) => i.Name === "PhoneNumber")?.Value ?? "");
    }

    const stkStatus     = resultCode === "0" ? "SUCCESS" : "FAILED";
    const paymentStatus = resultCode === "0" ? "PAID"    : "FAILED";

    // Update M-Pesa transaction
    await prisma.mpesaTransaction.update({
      where: { checkoutRequestId },
      data: {
        resultCode,
        resultDesc,
        mpesaReceiptNumber: receiptNumber,
        transactionDate,
        phoneNumber:        phoneNumber ? String(phoneNumber) : null,
        status:             stkStatus,
      },
    });

    // Update the parent order
    await prisma.order.update({
      where:  { id: txn.orderId },
      data:   { paymentStatus },
    });

    console.log(
      `[Mpesa Callback] Order ${txn.orderId} → ${paymentStatus}` +
      (receiptNumber ? ` | Receipt: ${receiptNumber}` : ""),
    );
  } catch (err) {
    console.error("[Mpesa Callback] Processing error:", err);
  }
});

export default router;
