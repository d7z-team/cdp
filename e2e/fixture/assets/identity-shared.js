importScripts('/assets/identity.js');
onconnect = event => {
  const port = event.ports[0];
  port.onmessage = () => identity().then(value => port.postMessage(value));
  port.start();
};
