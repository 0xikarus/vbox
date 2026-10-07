 (() => {
  "use strict";

  const MARK = "data-vmbox-captcha";
  const TAG = "vmbox-captcha-guard";

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
      'form img[src*="captcha"]',
      'input[name*="captcha"]'
    ]]
  ];

  // One widget entry per matching element, not just the first hit: the popup
  // shows the raw data of every captcha on the page.
  const collect = () => {
    const found = [];
    for (const [type, selectors] of SIGNATURES) {
      for (const selector of selectors) {
        let elements;
        try {
          elements = document.querySelectorAll(selector);
        } catch (_) {
          return found;
        }
        for (const element of elements) {
          const box = element.getBoundingClientRect();
          found.push({
            type,
            selector,
            sitekey: sitekeyFor(type, element) || "",
            rect: { x: Math.round(box.x), y: Math.round(box.y), width: Math.round(box.width), height: Math.round(box.height) }
          });
        }
      }
    }
    return found;
  };

  const WIDGET_CONTAINERS = {
    recaptcha: ".g-recaptcha",
    hcaptcha: ".h-captcha",
    turnstile: ".cf-turnstile"
  };

  const WIDGET_FRAMES = {
    recaptcha: 'iframe[src*="google.com/recaptcha"],iframe[src*="recaptcha/api2"],iframe[src*="recaptcha/enterprise"]',
    hcaptcha: 'iframe[src*="hcaptcha.com"]',
    turnstile: 'iframe[src*="challenges.cloudflare.com"]'
  };

  // The site key is public widget configuration: it is what lets the chat UI
  // re-render the same challenge for the owner. It is not a secret or a token.
  const sitekeyFor = (type, element) => {
    if (element && element.dataset && element.dataset.sitekey) return element.dataset.sitekey;
    try {
      const container = document.querySelector(WIDGET_CONTAINERS[type] || "");
      if (container && container.dataset && container.dataset.sitekey) return container.dataset.sitekey;
    } catch (_) {}
    try {
      const frame = document.querySelector(WIDGET_FRAMES[type] || "");
      const src = frame && frame.src;
      if (src) {
        const parsed = new URL(src);
        return parsed.searchParams.get("sitekey") || parsed.searchParams.get("key") || "";
      }
    } catch (_) {}
    return "";
  };

  const detectWithRect = () => {
    for (const [type, selectors] of SIGNATURES) {
      for (const selector of selectors) {
        try {
          const element = document.querySelector(selector);
          if (element) {
            const box = element.getBoundingClientRect();
            return { type, rect: { x: box.x, y: box.y, width: box.width, height: box.height } };
          }
        } catch (_) {
          return null;
        }
      }
    }
    return null;
  };

  const clear = () => {
    document.documentElement.removeAttribute(MARK);
  };

  const apply = (type, rect, sitekey) => {
    if (!type) {
      clear();
      return;
    }
    document.documentElement.setAttribute(MARK, JSON.stringify({ type, ts: Date.now(), rect: rect || null, sitekey: sitekey || "" }));
  };

  const scan = () => {
    const found = detectWithRect();
    apply(found ? found.type : null, found ? found.rect : null, found ? sitekeyFor(found.type) : "");
  };

  // The popup asks the active tab for the raw widget list.
  if (typeof chrome !== "undefined" && chrome.runtime && chrome.runtime.onMessage) {
    chrome.runtime.onMessage.addListener((_request, _sender, respond) => {
      respond({ url: location.href, widgets: collect() });
    });
  }

  window.addEventListener("message", (event) => {
    if (event.source !== window.top && event.data && event.data.source === TAG && typeof event.data.type === "string") {
      apply(event.data.type, event.data.rect || null, event.data.sitekey || "");
    }
  });

  const report = () => {
    const found = detectWithRect();
    const type = found ? found.type : "";
    if (window.top === window) {
      apply(type, found ? found.rect : null, found ? sitekeyFor(type) : "");
    } else if (type) {
      try {
        window.top.postMessage({ source: TAG, type, rect: found.rect, sitekey: sitekeyFor(type) }, "*");
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
