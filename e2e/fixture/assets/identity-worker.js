importScripts('/assets/identity.js');
onmessage = () => identity().then(value => postMessage(value));
