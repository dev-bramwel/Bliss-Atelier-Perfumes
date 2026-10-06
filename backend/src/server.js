import "dotenv/config";
import express from "express";
import cors from "cors";

import ordersRouter from "./routes/orders.js";
import mpesaRouter from "./routes/mpesa.js";

const app = express();
const PORT = process.env.PORT || 5000;

// --------------------
// MIDDLEWARE
// --------------------
app.use(
  cors({
    // In production lock this down to your actual frontend origin
    origin: process.env.FRONTEND_ORIGIN || "*",
  }),
);
app.use(express.json());

// --------------------
// ROUTES
// --------------------
app.get("/", (_req, res) => res.json({ status: "Bliss Atelier API running" }));
app.use("/api/orders", ordersRouter);
app.use("/api/mpesa", mpesaRouter);

// --------------------
// START
// --------------------
app.listen(PORT, () => {
  console.log(`[server] listening on http://localhost:${PORT}`);
  console.log(`[server] M-Pesa env: ${process.env.MPESA_ENV ?? "sandbox"}`);
});
