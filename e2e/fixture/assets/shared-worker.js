onconnect = event => {
  const port = event.ports[0];
  port.onmessage = message => port.postMessage(message.data * 2);
  port.start();
};
