self.importScripts('./crypto-js.min.js');

function sha256Uint8(encPwd, nonce) {
  const hex = CryptoJS.SHA256(encPwd + nonce).toString(CryptoJS.enc.Hex);
  const bytes = new Uint8Array(hex.length >>> 1);
  for (let i = 0, p = 0; i < bytes.length; i++, p += 2) {
    bytes[i] = parseInt(hex.substr(p, 2), 16);
  }
  return bytes;
}

function prefixZero(buf, bits) {
  const full = bits >>> 3;
  const rem = bits & 7;
  for (let i = 0; i < full; i++) if (buf[i]) return false;
  return rem ? (buf[full] & (0xFF << (8 - rem))) === 0 : true;
}

self.onmessage = ({ data }) => {
  const { encPwd, bits, timeout } = data;
  const deadline = Date.now() + timeout;
  let nonce = (Math.random() * 0xFFFFFFFF) >>> 0;
  while (Date.now() < deadline) {
    if (prefixZero(sha256Uint8(encPwd, nonce), bits)) {
      self.postMessage({ ok: true, nonce });
      return;
    }
    nonce = (nonce + 1) >>> 0;
    if ((nonce & 0x7FFF) === 0) self.postMessage({ progress: true });
  }
  self.postMessage({ ok: false });
};
