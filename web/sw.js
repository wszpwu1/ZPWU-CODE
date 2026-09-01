const CACHE_NAME = 'zpwu-code-v1';
const ASSETS = ['/', '/index.html', '/styles.css', '/app.js', '/manifest.webmanifest'];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE_NAME).then((cache) => cache.addAll(ASSETS)));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys();
      await Promise.allSettled(keys.filter((key) => key !== CACHE_NAME).map((key) => caches.delete(key)));
      await self.clients.claim();
    })()
  );
});

self.addEventListener('fetch', (event) => {
  if (event.request.mode === 'navigate') {
    const networkResponse = fetch(event.request);
    const responsePromise = networkResponse
      .then((response) => response)
      .catch(() => caches.match('/index.html'));
    const cacheWritePromise = networkResponse
      .then((response) => caches.open(CACHE_NAME).then((cache) => cache.put('/index.html', response.clone())))
      .catch(() => Promise.resolve());

    event.respondWith(responsePromise);
    event.waitUntil(cacheWritePromise);
    return;
  }

  const networkResponse = fetch(event.request);
  const cacheWritePromise = networkResponse
    .then((response) => {
      if (!response.ok) return Promise.resolve();
      return caches.open(CACHE_NAME).then((cache) => cache.put(event.request, response.clone()));
    })
    .catch(() => Promise.resolve());

  event.respondWith(caches.match(event.request).then((cached) => cached || networkResponse));
  event.waitUntil(cacheWritePromise);
});
