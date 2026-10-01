function messageTone(status) {
  switch (status) {
    case "read":
    case "played":
    case "delivered": return "success";
    case "sent": return "info";
    case "queued":
    case "sending": return "warning";
    case "failed": return "danger";
  }
  return "neutral";
}

function messageSummary(m) {
  const type = text(m.type, "text");
  if (type === "text") return text(m.body, "");
  if (type === "location") {
    const loc = m.location || {};
    return "Location " + text(loc.name, text(loc.lat, "") + ", " + text(loc.lng, ""));
  }
  const media = m.media || {};
  const label = type.charAt(0).toUpperCase() + type.slice(1);
  const name = text(media.filename, "");
  const caption = text(m.body, "");
  return [label, name, caption].filter(function (x) { return x !== ""; }).join(" · ");
}

function messageSendModel(d) {
  const m = d.message || {};
  const status = text(m.status, "queued");
  return {
    empty: !d.message,
    id: text(m.id, ""),
    status: status,
    badge: badgeColour(messageTone(status)),
    summary: messageSummary(m),
    error: status === "failed" ? text(m.error, "Sending failed.") : "",
    day: day(m.timestamp),
  };
}
