# Security policy

vbox runs agent boxes with credentials, desktops and shell access, so please
report vulnerabilities privately.

## Reporting a vulnerability

- Use GitHub's **Report a vulnerability** (private security advisory) on this
  repository, or contact the maintainer through their GitHub profile.
- Do not open a public issue for security problems.
- Include affected version/commit, impact, and steps to reproduce.

You can expect an acknowledgement within a few days. Please give us a
reasonable time to release a fix before disclosing details publicly.

## Scope

In scope: the controller, worker/box runtime, CLI, installer and the web UI in
this repository. Out of scope: third-party providers (Railway, Cloudflare,
Resend, model vendors) and your own deployment configuration.

## Handling secrets

Never commit credentials. Controller secrets (encryption key, provider tokens,
mail keys) belong in environment variables of your deployment.
