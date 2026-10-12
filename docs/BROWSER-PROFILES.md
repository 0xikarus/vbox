# Import Chromium browser sessions

`vbox profiles upload` discovers local Chrome and Chromium profiles on Windows
and Linux. It displays each profile's name and full filesystem path. Mark one
or more browser profiles and choose **Upload**. For each selected profile, the
CLI opens a local export page in that specific browser profile.

On Windows, run the current Linux CLI from WSL. It discovers Windows Chrome and
Chromium through `%LOCALAPPDATA%`, converts their paths with `wslpath`, and
opens the Windows browser through WSL interoperability. The controller's main
CLI does not yet build as a native Windows executable. Keep WSL localhost
forwarding enabled so Windows Chrome can reach the temporary loopback page.

The first time you export from a profile, open `chrome://extensions`, enable
Developer mode, choose **Load unpacked**, and select the helper directory shown
on the export page. Reload the page and click **Export and upload**. The helper
is bundled with the CLI and installed under your local vbox configuration
directory. Chrome asks for cookie and website access because the helper must
read the selected profile's sessions. The helper sends the export only to the
CLI's temporary loopback server. The CLI uploads it over the authenticated
controller connection; the controller stores it encrypted and returns only
name, source path, and origin metadata in list responses.

```text
vbox profiles upload
vbox profiles list-browsers
vbox profiles attach-browser chromium-default my-box
vbox profiles attach-browser chromium-default my-box --apply
```

An attached profile appears in the box's **Browser sessions** panel. Click
**Apply** there after Chromium is running, or use `--apply` on the CLI command.
Attaching does not wake a sleeping box. The saved profile can be attached to
multiple boxes. Reuploading the same saved name replaces the controller copy;
already attached and applied copies are unchanged.

The export includes HTTPS cookies and localStorage from open HTTPS tabs.
Domain cookies are retained. Partitioned cookies, IndexedDB, sessionStorage,
browser passwords, bookmarks, history, extensions, and passkeys are not
transferred. Some sites use device-bound sessions and may require a fresh
login in the box. The browser keeps its imported state on that box's persistent
volume. Removing a saved controller profile does not sign out existing boxes.

If Chrome or Chromium is installed outside the usual locations, set
`VMBOX_CHROME_BINARY` or `VMBOX_CHROMIUM_BINARY` to its executable path before
running the CLI.
