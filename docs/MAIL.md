# Mail for boxes

Boxes can receive mail at their own addresses and send mail after the owner
approves each message. The feature is off until the controller has a mail
domain (`VMBOX_MAIL_DOMAIN`); inbound delivery also answers 503 until the inbound
secret is set.

- **Inbound:** a catch-all on your mail domain hands each message to a
  Cloudflare Email Worker, which asks the controller whether the recipient
  exists and then posts the raw message. Unknown recipients are rejected during
  SMTP, so senders get a bounce.
- **Outbound:** agents draft mail into an outbox. Nothing is sent until the
  owner approves it; approved mail is sent through [Resend](https://resend.com).
- Agents subscribe to their inbox while running. Mail never wakes a hibernated
  box; a subscribed box receives its pending batch on its next start.

## Controller settings

| Variable | Purpose | Secret |
| --- | --- | --- |
| `VMBOX_MAIL_DOMAIN` | Domain for box addresses, for example `mail.example.com` | no |
| `VMBOX_INBOUND_MAIL_SECRET` | HMAC key shared with the inbound Worker | yes |
| `VMBOX_RESEND_API_KEY` | Send-only Resend API key for approved outbound mail | yes |

Inbound mail needs the domain and the inbound secret; outbound mail needs the
domain and the Resend key. Generate the inbound secret with, for example,
`openssl rand -hex 32`, and store it only in the controller environment and in
the Worker's secret storage.

## Inbound: Cloudflare Email Routing

1. Add the mail domain to Cloudflare and enable **Email Routing** on it.
   Cloudflare adds the MX and SPF records.
2. Create an Email Worker from
   [`deploy/cloudflare/inbound-mail-worker.js`](../deploy/cloudflare/inbound-mail-worker.js).
   Set `CONTROLLER_URL` to your public controller URL as a variable, and
   `INBOUND_MAIL_SECRET` to the controller's `VMBOX_INBOUND_MAIL_SECRET` as a
   secret.
3. Add a **catch-all** routing rule for the domain that sends mail to the
   Worker.

The Worker signs each request with
`hex(HMAC-SHA256(secret, timestamp + "." + body))` in `X-Vbox-Signature` and
`X-Vbox-Timestamp`. The controller rejects timestamps more than five minutes off
and ignores duplicate deliveries (`X-Vbox-Idempotency`). Messages over 25 MiB are rejected.

To check the wiring, an unsigned request must be refused:

```bash
curl -i -X POST https://YOUR-CONTROLLER/v1/inbound-mail/recipient -d '{}'
```

## Outbound: Resend

1. Add the mail domain in Resend and create the DKIM, SPF and DMARC records it
   lists.
2. Create a **send-only** API key restricted to that domain and set it as
   `VMBOX_RESEND_API_KEY`.

## Rotating secrets

Change `VMBOX_INBOUND_MAIL_SECRET` in the controller and the Worker at the same
time; mail that arrives between the two changes is retried by the sending
server. A new Resend key takes effect on the next approved message.
