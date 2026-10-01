function chatRow(c, action) {
  const id = text(c.id, "");
  const kind = text(c.kind, "dm");
  const unread = Number(c.unreadCount) || 0;
  return {
    id: id,
    name: text(c.name, text(String(c.jid || "").split("@")[0], "Unknown")),
    kind: kind === "group" ? "Group" : "Direct",
    preview: text(c.lastMessagePreview, ""),
    day: day(c.lastMessageAt),
    unread: unread > 0 ? num(unread) : "",
    archived: c.archived === true,
    reply: c.archived === true ? "" : action("reply:" + id, "message_send", { deviceId: text(c.deviceId, ""), to: text(c.jid, "") }),
  };
}

function chatListModel(d, action) {
  const chats = d.chats || [];
  const unread = chats.reduce(function (sum, c) { return sum + (Number(c.unreadCount) || 0); }, 0);
  const groups = chats.filter(function (c) { return c.kind === "group"; }).length;
  return {
    empty: chats.length === 0,
    more: text(d.nextCursor, "") !== "",
    kpis: [
      { label: "Chats", value: num(chats.length) },
      { label: "Groups", value: num(groups) },
      { label: "Unread", value: num(unread) },
    ],
    rows: chats.map(function (c) { return chatRow(c, action); }),
  };
}
