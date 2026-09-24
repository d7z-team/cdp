async function identity() {
  return {
    ua: navigator.userAgent,
    platform: navigator.platform,
    metadata: await navigator.userAgentData.getHighEntropyValues(['architecture','bitness','platformVersion','fullVersionList','model','wow64','formFactors']),
    headers: await (await fetch('/api/headers')).json()
  };
}
