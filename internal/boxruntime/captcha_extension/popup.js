(async () => {
  const urlBox = document.getElementById("page-url");
  const widgetsBox = document.getElementById("widgets");
  try {
    let tabs = await chrome.tabs.query({ active: true, currentWindow: true });
    let page = tabs.find(tab => tab.url && !tab.url.startsWith("chrome-extension://") && !tab.url.startsWith("chrome://"));
    if (!page) {
      const all = await chrome.tabs.query({});
      page = all.reverse().find(tab => tab.url && tab.url.startsWith("http"));
    }
    if (!page || page.id === undefined) {
      widgetsBox.textContent = "No active tab.";
      return;
    }
    const tab = page;
    const response = await chrome.tabs.sendMessage(tab.id, { source: "vmbox-captcha-guard", request: "widgets" }).catch(() => null);
    if (!response) {
      widgetsBox.innerHTML = '<div class="none">No detection data on this page. Reload the page to scan.</div>';
      return;
    }
    urlBox.textContent = (response.url || tab.url || "");
    const widgets = Array.isArray(response.widgets) ? response.widgets : [];
    if (!widgets.length) {
      widgetsBox.innerHTML = '<div class="none">No captcha detected on this page.</div>';
      return;
    }
    widgetsBox.replaceChildren(...widgets.map(widget => {
      const card = document.createElement("div");
      card.className = "card";
      const title = document.createElement("h2");
      title.textContent = widget.type + (widget.selector ? "  ·  " + widget.selector : "");
      const raw = document.createElement("pre");
      raw.textContent = JSON.stringify(widget, null, 2);
      card.append(title, raw);
      return card;
    }));
  } catch (error) {
    widgetsBox.textContent = "Error: " + error;
  }
})();
