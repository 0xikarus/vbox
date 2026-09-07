# Native desktop CLI verification — 2026-09-07

Implemented `vmbox desktop BOX [--enable] [--no-viewer] [--viewer PATH]`.
Terminal attach remains `vmbox BOX`. Both graphical clients use the same remote
desktop; the CLI carries VNC over direct SSH instead of the controller websocket.

## Live evidence

- Disposable, credentials-free `cli-vnc-proof-0907` started hibernated. The CLI
  resumed it, explicitly enabled desktop packages, and opened a real TigerVNC
  1.12 viewer in a local Docker/Xvfb desktop.
- Viewer negotiated RFB 3.8 with the loopback tunnel. Screenshot
  `/tmp/vmbox-cli-desktop-proof/viewer.png` was visually inspected: it shows the
  remote Firefox desktop in a TigerVNC window, not a mocked framebuffer.
- Alt-F4 closed the actual viewer; CLI exited 0. An independent connection to
  the local port then returned ECONNREFUSED.
- Independent controller/SSH checks after viewer exit found the box running,
  `vmbox-desktop` still present, and Xtigervnc PID 2926 alive.
- Cleanup targets only this disposable box and its test volume. User boxes and
  the existing four-slot fleet capacity are not changed.

## Automated checks and limits

Full Go tests and vet passed. Tests cover binary loopback delivery, single-client
tunnel closure, cancellation, early viewer exit, controller resume/enable/start,
assignment fencing, and missing-viewer failure before compute wake-up.

Live native-viewer coverage is Linux/TigerVNC with an automated X display.
Physical laptop interaction, native OS clipboard integration, other VNC viewers,
and Windows/WSL GUI execution have not been live verified. A local viewer is a
dependency, not bundled or automatically installed. The tunnel listens only on
localhost and accepts one connection; use it on a trusted local machine.
