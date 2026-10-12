(() => {
  const match = location.pathname.match(/^\/vbox-browser-export\/([a-f0-9]{64})$/);
  if (!match) return;
  const button = document.querySelector('#export-browser-profile');
  const status = document.querySelector('#export-status');
  if (!button || !status) return;
  document.documentElement.dataset.vboxExtensionReady = 'true';
  button.disabled = false;
  button.addEventListener('click', async () => {
    button.disabled = true;
    status.textContent = 'Reading cookies and storage from open HTTPS tabs…';
    try {
      const result = await chrome.runtime.sendMessage({type: 'vbox-export-browser-state'});
      if (!result || result.error) throw new Error(result?.error || 'Export failed');
      const response = await fetch(location.pathname + '/ingest', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(result.state), cache: 'no-store'
      });
      if (!response.ok) throw new Error((await response.text()).slice(0, 300));
      status.textContent = `Sent ${result.state.origins.length} sites to the local vbox CLI. You can close this tab.`;
    } catch (error) {
      status.textContent = error.message || String(error);
      button.disabled = false;
    }
  });
})();
