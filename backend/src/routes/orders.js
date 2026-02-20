import { Router } from "express";
import { PrismaClient } from "../../generated/prisma/index.js";
import { initiateStkPush } from "../services/mpesa.js";

const router  = Router();
const prisma  = new PrismaClient();

// --------------------
// POST /api/orders
// Create order; trigger STK push when paymentMethod = "pay_now"
// --------------------
router.post("/", async (req, res) => {
  try {
    const { customer, items, totalKes } = req.body;

    if (!customer?.name || !customer?.phone || !customer?.location) {
      return res.status(400).json({ error: "Missing required customer fields." });
    }
    if (!Array.isArray(items) || items.length === 0) {
      return res.status(400).json({ error: "Order must contain at least one item." });
    }

    // Persist the order
    const order = await prisma.order.create({
      data: {
        customerName:     customer.name,
        customerPhone:    customer.phone,
        deliveryLocation: customer.location,
        deliveryOption:   customer.delivery   || "delivery",
        notes:            customer.notes      || null,
        paymentMethod:    customer.payment    || "on_delivery",
        paymentStatus:    "PENDING",
        totalKes:         Number(totalKes),
        items,
      },
    });

    // Trigger STK push only for pay_now orders
    if (order.paymentMethod === "pay_now") {
      let stkResult;
      try {
        stkResult = await initiateStkPush(customer.phone, totalKes, order.id);
      } catch (mpesaErr) {
        // If STK push fails we still want the order saved — surface the error but
        // don't rollback so the merchant can follow up manually.
        console.error("[M-Pesa STK] initiation failed:", mpesaErr?.response?.data ?? mpesaErr.message);
        return res.status(202).json({
          order,
          stkPush: null,
          warning: "Order saved but M-Pesa STK initiation failed. Check your Daraja credentials.",
        });
      }

      // Store the STK transaction record
      await prisma.mpesaTransaction.create({
        data: {
          orderId:          order.id,
          merchantRequestId: stkResult.merchantRequestId,
          checkoutRequestId: stkResult.checkoutRequestId,
          status:           "PENDING",
        },
      });

      return res.status(201).json({ order, stkPush: stkResult });
    }

    // on_delivery — just return the saved order
    return res.status(201).json({ order, stkPush: null });
  } catch (err) {
    console.error("[POST /api/orders]", err);
    res.status(500).json({ error: "Internal server error." });
  }
});

// --------------------
// GET /api/orders/:id/payment-status
// Frontend polls this to know when the STK callback has been received
// --------------------
router.get("/:id/payment-status", async (req, res) => {
  try {
    const order = await prisma.order.findUnique({
      where: { id: req.params.id },
      include: { mpesaTransaction: true },
    });
    if (!order) return res.status(404).json({ error: "Order not found." });

    res.json({
      orderId:       order.id,
      paymentStatus: order.paymentStatus,
      mpesaStatus:   order.mpesaTransaction?.status ?? null,
      receiptNumber: order.mpesaTransaction?.mpesaReceiptNumber ?? null,
    });
  } catch (err) {
    console.error("[GET /api/orders/:id/payment-status]", err);
    res.status(500).json({ error: "Internal server error." });
  }
});

export default router;
