// SECURITY: only base64 characters reach the data: URL, so a tool result cannot smuggle markup or a second URL into src.
const PNG_BASE64 = /^[A-Za-z0-9+/]+={0,2}$/;

function pairModel(deviceId, link, device, action) {
  const l = link || {};
  const dev = device || {};
  const id = text(deviceId, "");
  const outcome = text(l.outcome, "none");
  const png = typeof l.png === "string" && PNG_BASE64.test(l.png) ? "data:image/png;base64," + l.png : "";
  return {
    deviceId: id,
    outcome: outcome,
    pending: outcome === "pending",
    qr: png,
    code: text(l.pairingCode, ""),
    state: text(dev.state, ""),
    phone: dev.phone ? "+" + text(dev.phone, "") : "",
    refresh: id ? action("refresh:" + id, "device_get", { deviceId: id }) : "",
  };
}

function devicePairModel(d, action) {
  return pairModel(d.deviceId, d.link, null, action);
}

function deviceGetModel(d, action) {
  const dev = d.device || {};
  return pairModel(dev.id, dev.link, dev, action);
}
