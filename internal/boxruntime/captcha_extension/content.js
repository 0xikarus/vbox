(() => {
  "use strict";

  const MARK = "data-vmbox-captcha";
  const TAG = "vmbox-captcha-guard";
  const BANNER_ID = "__vmbox_captcha_banner";

  const SIGNATURES = [
    ["recaptcha", [
      'iframe[src*="google.com/recaptcha"]',
      'iframe[src*="recaptcha/api2"]',
      'iframe[src*="recaptcha/enterprise"]',
      ".g-recaptcha"
    ]],
    ["hcaptcha", [
      'iframe[src*="hcaptcha.com"]',
      ".h-captcha"
    ]],
    ["turnstile", [
      'iframe[src*="challenges.cloudflare.com"]',
      ".cf-turnstile"
    ]],
    ["image", [
      'img[src*="captcha"]',
      'input[name*="captcha"]'
    ]]
  ];

  const detect = () => {
    for (const [type, selectors] of SIGNATURES) {
      for (const selector of selectors) {
        try {
          if (document.querySelector(selector)) return type;
        } catch (_) {
          return null;
        }
      }
    }
    return null;
  };

  const clear = () => {
    document.documentElement.removeAttribute(MARK);
    const banner = document.getElementById(BANNER_ID);
    if (banner) banner.remove();
  };

  const showBanner = (type) => {
    if (document.getElementById(BANNER_ID)) return;
    const banner = document.createElement("div");
    banner.id = BANNER_ID;
    banner.setAttribute("role", "status");
    banner.textContent = "CAPTCHA detected (" + type + "). Your agent is waiting for you to solve it.";
    banner.style.cssText = "position:fixed;right:12px;bottom:12px;z-index:2147483647;max-width:320px;" +
      "padding:10px 12px;border-radius:8px;font:13px/1.4 system-ui,sans-serif;color:#fff;" +
      "background:rgba(17,24,39,.94);box-shadow:0 2px 10px rgba(0,0,0,.4);pointer-events:none";
    (document.body || document.documentElement).appendChild(banner);
  };

  const apply = (type) => {
    if (!type) {
      clear();
      return;
    }
    document.documentElement.setAttribute(MARK, JSON.stringify({ type, ts: Date.now() }));
    showBanner(type);
  };

  const scan = () => apply(detect());

  window.addEventListener("message", (event) => {
    if (event.source !== window.top && event.data && event.data.source === TAG && typeof event.data.type === "string") {
      apply(event.data.type);
    }
  });

  const report = () => {
    const type = detect();
    if (window.top === window) {
      apply(type);
    } else if (type) {
      try {
        window.top.postMessage({ source: TAG, type }, "*");
      } catch (_) {}
    } else {
      try {
        window.top.postMessage({ source: TAG, type: "" }, "*");
      } catch (_) {}
    }
  };

  let scheduled = false;
  const schedule = () => {
    if (scheduled) return;
    scheduled = true;
    setTimeout(() => {
      scheduled = false;
      scan();
    }, 250);
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", report, { once: true });
  } else {
    report();
  }

  new MutationObserver(schedule).observe(document.documentElement, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["src", "class", "id"]
  });

  window.addEventListener("message", (event) => {
    if (event.source === window.top && event.data && event.data.source === TAG) schedule();
  });
})();
