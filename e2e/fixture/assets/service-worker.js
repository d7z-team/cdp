oninstall = () => skipWaiting();
onactivate = event => event.waitUntil(clients.claim());
onmessage = event => event.ports[0].postMessage(event.data * 2);
