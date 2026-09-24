onmessage = event => {
  const child = new Worker('worker.js');
  child.onmessage = result => { postMessage(result.data); child.terminate(); };
  child.postMessage(event.data);
};
