'use strict';
/* Web push for vmbox agent chats: shows a notification when an agent replies
   and focuses the matching chat on click. */
self.addEventListener('push', event => {
  let data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) { /* fall through with defaults */ }
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const client of windows) {
      try { client.postMessage({ type: 'vmbox-push', box: data.box || '' }); } catch (e) { /* ignore dead clients */ }
    }
    const open = windows.find(client => client.visibilityState === 'visible' && client.url.includes('/chat') && client.url.includes('box=' + (data.box || '%none%')));
    if (open) { try { await open.focus(); } catch (e) { /* focus is best-effort */ } return; }
    await self.registration.showNotification(data.title || 'vmbox agent', {
      body: data.body || 'New reply',
      tag: 'vmbox-' + (data.box || 'chat'),
      renotify: true,
      icon: '/favicon.svg',
      badge: '/favicon.svg',
      data: { url: '/chat#box=' + encodeURIComponent(data.box || '') }
    });
  })());
});
self.addEventListener('notificationclick', event => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || '/chat';
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    const existing = windows.find(client => client.url.includes('/chat'));
    if (existing) {
      try { existing.postMessage({ type: 'vmbox-open', url }); } catch (e) { /* ignore */ }
      return existing.focus();
    }
    return self.clients.openWindow(url);
  })());
});
