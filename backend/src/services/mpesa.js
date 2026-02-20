import axios from "axios";

// --------------------
// CONFIG
// --------------------
const ENV         = process.env.MPESA_ENV || "sandbox";
const BASE_URL    = ENV === "production"
  ? "https://api.safaricom.co.ke"
  : "https://sandbox.safaricom.co.ke";

const CONSUMER_KEY    = process.env.MPESA_CONSUMER_KEY;
const CONSUMER_SECRET = process.env.MPESA_CONSUMER_SECRET;
const SHORTCODE       = process.env.MPESA_SHORTCODE;
const PASSKEY         = process.env.MPESA_PASSKEY;
const CALLBACK_URL    = process.env.MPESA_CALLBACK_URL;

// --------------------
// HELPERS
// --------------------

/** Base64 encode consumer key + secret to get the OAuth token */
async function getAccessToken() {
  const credentials = Buffer.from(`${CONSUMER_KEY}:${CONSUMER_SECRET}`).toString("base64");
  const res = await axios.get(`${BASE_URL}/oauth/v1/generate?grant_type=client_credentials`, {
    headers: { Authorization: `Basic ${credentials}` },
  });
  return res.data.access_token;
}

/** Generate the base64 password used in STK push requests */
function generatePassword(timestamp) {
  const raw = `${SHORTCODE}${PASSKEY}${timestamp}`;
  return Buffer.from(raw).toString("base64");
}

/** Format a Kenyan phone number to the 2547XXXXXXXX format Safaricom expects */
export function formatPhone(phone) {
  // Strip spaces, dashes, plus signs
  let p = String(phone).replace(/[\s\-+]/g, "");
  // 07XXXXXXXX → 2547XXXXXXXX
  if (p.startsWith("0")) p = "254" + p.slice(1);
  // +254 already stripped above; 254XXXXXXXXX passes through
  return p;
}

// --------------------
// STK PUSH
// --------------------

/**
 * Initiates an M-Pesa STK Push (Lipa Na M-Pesa Online).
 *
 * @param {string} phone   - Customer phone number (07xx… or 2547xx…)
 * @param {number} amount  - Amount in KES (integer)
 * @param {string} orderId - Your internal order ID (used as AccountReference)
 * @returns {Promise<{ merchantRequestId: string, checkoutRequestId: string, responseDescription: string }>}
 */
export async function initiateStkPush(phone, amount, orderId) {
  const token     = await getAccessToken();
  const timestamp = new Date()
    .toISOString()
    .replace(/[^0-9]/g, "")
    .slice(0, 14);             // YYYYMMDDHHmmss
  const password  = generatePassword(timestamp);

  const payload = {
    BusinessShortCode: SHORTCODE,
    Password:          password,
    Timestamp:         timestamp,
    TransactionType:   "CustomerPayBillOnline",
    Amount:            Math.ceil(amount),
    PartyA:            formatPhone(phone),
    PartyB:            SHORTCODE,
    PhoneNumber:       formatPhone(phone),
    CallBackURL:       CALLBACK_URL,
    AccountReference:  orderId,
    TransactionDesc:   `Bliss Atelier Order ${orderId}`,
  };

  const res = await axios.post(
    `${BASE_URL}/mpesa/stkpush/v1/processrequest`,
    payload,
    { headers: { Authorization: `Bearer ${token}` } },
  );

  return {
    merchantRequestId:  res.data.MerchantRequestID,
    checkoutRequestId:  res.data.CheckoutRequestID,
    responseDescription: res.data.ResponseDescription,
  };
}
