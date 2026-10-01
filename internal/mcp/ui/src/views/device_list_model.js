function deviceTone(state) {
  switch (state) {
    case "connected": return "success";
    case "linking": return "info";
    case "disconnected": return "warning";
    case "logged_out": return "danger";
  }
  return "neutral";
}

function deviceRow(d, action) {
  const id = text(d.id, "");
  const state = text(d.state, "unlinked");
  const paired = state === "connected" || state === "disconnected";
  return {
    id: id,
    badge: badgeColour(deviceTone(state)),
    name: text(d.name, "Unnamed"),
    state: state.replace("_", " "),
    phone: d.phone ? "+" + text(d.phone, "") : "—",
    seen: day(d.lastSeenAt),
    pair: paired ? "" : action("pair:" + id, "device_pair", { deviceId: id }),
  };
}

function deviceListModel(d, action) {
  const devices = d.devices || [];
  const connected = devices.filter(function (x) { return x.state === "connected"; }).length;
  const attention = devices.filter(function (x) { return x.state === "disconnected" || x.state === "logged_out"; }).length;
  return {
    empty: devices.length === 0,
    kpis: [
      { label: "Devices", value: num(devices.length) },
      { label: "Connected", value: num(connected) },
      { label: "Needs attention", value: num(attention) },
    ],
    rows: devices.map(function (x) { return deviceRow(x, action); }),
  };
}
