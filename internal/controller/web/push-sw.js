'use strict';
/* Web push for owner and box-to-box chats. */
self.addEventListener('install', event => event.waitUntil(self.skipWaiting()));
self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));
function destination(data){
  const fallback='/chat#box='+encodeURIComponent(data.box||'');
  const target=new URL(data.url||fallback,self.location.origin);
  return target.origin===self.location.origin&&['/chat','/box-chats'].includes(target.pathname)?target.href:new URL(fallback,self.location.origin).href;
}
self.addEventListener('push', event => {
  let data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) { /* fall through with defaults */ }
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const client of windows) {
      try { client.postMessage({ type: 'vmbox-push', box: data.box || '' }); } catch (e) { /* ignore dead clients */ }
    }
    const url=destination(data);
    const open = windows.find(client => client.visibilityState === 'visible' && client.url === url);
    if (open) { try { await open.focus(); } catch (e) { /* focus is best-effort */ } return; }
    await self.registration.showNotification(data.title || 'vmbox agent', {
      body: data.body || 'New reply',
      tag: 'vmbox-' + (data.box || data.url || 'chat'),
      renotify: true,
      icon: '/favicon.svg',
      badge: '/favicon.svg',
      data: { url }
    });
  })());
});
self.addEventListener('notificationclick', event => {
  event.notification.close();
  const url = destination({url:(event.notification.data && event.notification.data.url) || '/chat'});
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    const pathname=new URL(url).pathname;
    const existing = windows.find(client => new URL(client.url).pathname===pathname);
    if (existing) {
      try { await existing.navigate(url); } catch (e) { /* ignore and open another window */ }
      try { return await existing.focus(); } catch (e) { /* fall through */ }
    }
    return self.clients.openWindow(url);
  })());
});
