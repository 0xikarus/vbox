async function exportBrowserState() {
  const origins = new Map();
  const site = origin => {
    let value = origins.get(origin);
    if (!value) { value = {origin, cookies: [], localStorage: []}; origins.set(origin, value); }
    return value;
  };
  const cookies = await chrome.cookies.getAll({});
  for (const cookie of cookies) {
    // The current import format intentionally skips partitioned cookies.
    if (cookie.partitionKey) continue;
    const host = cookie.domain.replace(/^\./, '').toLowerCase();
    if (!host || host.includes(':') || !host.includes('.')) continue;
    const origin = `https://${host}`;
    const value = {
      name: cookie.name, value: cookie.value, path: cookie.path || '/',
      httpOnly: Boolean(cookie.httpOnly)
    };
    if (!cookie.hostOnly) value.domain = '.' + host;
    if (cookie.sameSite === 'strict') value.sameSite = 'Strict';
    if (cookie.sameSite === 'lax') value.sameSite = 'Lax';
    if (cookie.sameSite === 'no_restriction') value.sameSite = 'None';
    if (!cookie.session && Number.isFinite(cookie.expirationDate)) value.expires = cookie.expirationDate;
    site(origin).cookies.push(value);
  }
  const tabs = await chrome.tabs.query({});
  for (const tab of tabs) {
    if (!tab.id || !tab.url?.startsWith('https://')) continue;
    try {
      const result = await chrome.scripting.executeScript({target: {tabId: tab.id}, func: () => {
        const values = [];
        for (let i = 0; i < localStorage.length; i++) {
          const name = localStorage.key(i);
          values.push({name, value: localStorage.getItem(name)});
        }
        return {origin: location.origin, values};
      }});
      const data = result?.[0]?.result;
      if (data?.origin?.startsWith('https://') && Array.isArray(data.values) && data.values.length) {
        const target = site(data.origin);
        // Multiple tabs from one origin see the same localStorage.
        if (!target.localStorage.length) target.localStorage = data.values;
      }
    } catch (_) { /* A browser-owned or restricted tab cannot be read. */ }
  }
  const state = {version: 2, origins: [...origins.values()].sort((a, b) => a.origin.localeCompare(b.origin))};
  if (!state.origins.length) throw new Error('No HTTPS cookies or open-tab localStorage found in this browser profile.');
  return state;
}

chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  if (message?.type !== 'vbox-export-browser-state') return;
  exportBrowserState().then(state => respond({state}), error => respond({error: error.message || String(error)}));
  return true;
});
