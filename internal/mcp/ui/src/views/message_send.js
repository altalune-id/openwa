class MessageSendView extends LitElement {
  static properties = { model: { attribute: false } };
  static styles = appStyles;

  render() {
    const m = this.model;
    if (!m || m.empty) return html`<p class="app-muted">No message was queued.</p>`;
    return html`<div class="app-card app-stack">
      <div class="app-row">
        <span class="app-badge" style="background:${m.badge}"></span>
        <span class="app-title">${m.status}</span>
        <span class="app-muted app-num">${m.day}</span>
      </div>
      <p class="app-body">${m.summary}</p>
      ${m.error ? html`<p class="app-error">${m.error}</p>` : nothing}
      <p class="app-muted">Message ${m.id}. Status updates arrive by webhook, or call message_list with the chat.</p>
    </div>`;
  }
}

customElements.define("openwa-message-send", MessageSendView);

registerView("message_send", messageSendModel, (m) => html`<openwa-message-send .model=${m}></openwa-message-send>`);
