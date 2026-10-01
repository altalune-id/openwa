class ChatListView extends LitElement {
  static properties = { model: { attribute: false } };
  static styles = appStyles;

  // NOTE: submit does not cross the shadow root, so the form handles it here.
  submit(id, ev) {
    ev.preventDefault();
    const form = ev.currentTarget;
    if (!form.reportValidity()) return;
    this.dispatchEvent(new CustomEvent("openwa-action", {
      bubbles: true,
      composed: true,
      detail: { id: id, el: form, form: form },
    }));
  }

  row(r) {
    return html`<div class="app-chat">
      <div class="app-row">
        <span class="app-title">${r.name}</span>
        <span class="app-muted">${r.kind}</span>
        ${r.unread ? html`<span class="app-pill">${r.unread}</span>` : nothing}
        <span class="app-muted app-num">${r.day}</span>
      </div>
      ${r.preview ? html`<p class="app-muted app-clip">${r.preview}</p>` : nothing}
      ${r.reply
        ? html`<form class="app-inline" @submit=${(ev) => this.submit(r.reply, ev)}>
            <input class="app-input" name="text" placeholder="Reply" aria-label="Reply to ${r.name}" required>
            <button type="submit" class="app-action">Send</button>
          </form>`
        : nothing}
    </div>`;
  }

  render() {
    const m = this.model;
    if (!m || m.empty) return html`<p class="app-muted">No chats yet. Messages appear here once a paired device sends or receives one.</p>`;
    return html`<div class="app-stack">
      <div class="app-card app-kpis">
        ${m.kpis.map((k) => html`<div>
          <div class="app-kpi-label">${k.label}</div>
          <div class="app-kpi-value">${k.value}</div>
        </div>`)}
      </div>
      <div class="app-card">${m.rows.map((r) => this.row(r))}</div>
      ${m.more ? html`<p class="app-muted">More chats exist; call chat_list with the returned nextCursor.</p>` : nothing}
    </div>`;
  }
}

customElements.define("openwa-chat-list", ChatListView);

registerView("chat_list", chatListModel, (m) => html`<openwa-chat-list .model=${m}></openwa-chat-list>`);
