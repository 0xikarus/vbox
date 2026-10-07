// vbox-inbound-mail: Cloudflare Email Worker that forwards mail for the
// controller's mail domain to POST /v1/inbound-mail. Set CONTROLLER_URL as a
// variable and INBOUND_MAIL_SECRET as a secret (same value as the controller's
// VMBOX_INBOUND_MAIL_SECRET).
// Signature: hex(HMAC-SHA256(INBOUND_MAIL_SECRET, bytes(timestamp + ".") || body)).
const MAX_BYTES = 25 * 1024 * 1024;
const enc = new TextEncoder();

async function sign(secret, ts, body) {
  const key = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const prefix = enc.encode(ts + ".");
  const data = new Uint8Array(prefix.length + body.byteLength);
  data.set(prefix, 0);
  data.set(new Uint8Array(body), prefix.length);
  const mac = await crypto.subtle.sign("HMAC", key, data);
  return [...new Uint8Array(mac)].map(b => b.toString(16).padStart(2, "0")).join("");
}

async function post(env, path, body, headers) {
  const ts = Math.floor(Date.now() / 1000).toString();
  const signature = await sign(env.INBOUND_MAIL_SECRET, ts, body);
  return fetch(env.CONTROLLER_URL.replace(/\/$/, "") + path, {
    method: "POST",
    body,
    headers: { ...headers, "X-Vbox-Timestamp": ts, "X-Vbox-Signature": signature },
  });
}

async function digest(bytes) {
  const hash = await crypto.subtle.digest("SHA-256", bytes);
  return [...new Uint8Array(hash)].map(b => b.toString(16).padStart(2, "0")).join("");
}

export default {
  async email(message, env) {
    if (!env.INBOUND_MAIL_SECRET || !env.CONTROLLER_URL) throw new Error("worker not configured");
    if (message.rawSize > MAX_BYTES) { message.setReject("Message too large"); return; }
    const to = message.to.toLowerCase();
    const lookup = await post(env, "/v1/inbound-mail/recipient", enc.encode(JSON.stringify({ to })), { "Content-Type": "application/json" });
    if (lookup.status === 404) { message.setReject("Unknown recipient"); return; }
    // Any other failure throws, so the sending server retries later.
    if (!lookup.ok) throw new Error("recipient lookup failed: " + lookup.status);
    const raw = await new Response(message.raw).arrayBuffer();
    const idempotency = (message.headers.get("Message-ID") || "") + "|" + to + "|" + (await digest(raw));
    const delivered = await post(env, "/v1/inbound-mail", raw, {
      "Content-Type": "message/rfc822",
      "X-Vbox-Rcpt": to,
      "X-Vbox-From": message.from,
      "X-Vbox-Idempotency": await digest(enc.encode(idempotency)),
    });
    if (delivered.status === 404) { message.setReject("Unknown recipient"); return; }
    if (!delivered.ok) throw new Error("delivery failed: " + delivered.status);
  },
};
