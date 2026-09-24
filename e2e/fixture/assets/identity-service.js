importScripts('/assets/identity.js');
oninstall = () => skipWaiting();
onactivate = event => event.waitUntil(clients.claim());
onmessage = event => event.waitUntil(identity().then(value => event.ports[0].postMessage(value)));
