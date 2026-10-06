# Contributing

Thanks for your interest in vbox!

## Getting started

- Go toolchain as declared in `go.mod`, Node.js for the browser tests, and
  Chromium for Puppeteer (`VMBOX_CHROMIUM=/path/to/chromium`).
- Self-hosting and development setup: see `README.md` and
  `docs/LINUX-VPS-SETUP.md`.

## Before opening a pull request

1. Keep changes focused; one topic per pull request.
2. Run the checks locally (there is no hosted CI):

   ```sh
   scripts/check.sh            # build, vet, model checks, controller tests
   scripts/check.sh --browser  # plus the serial browser suite
   ```

3. Add or update tests for behavior changes (Go tests next to the code,
   browser tests in `tests/browser`).
4. Do not commit secrets, personal data, or generated artifacts.

## License

By contributing, you agree that your contributions are licensed under the
Apache License, Version 2.0 (see `LICENSE`).
