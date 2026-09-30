class DevicePairView extends LitElement {
  static properties = { model: { attribute: false } };
  static styles = appStyles;

  fire(id, ev) {
    const el = ev.currentTarget;
    this.dispatchEvent(new CustomEvent("openwa-action", {
      bubbles: true,
      composed: true,
      detail: { id: id, el: el, form: el.closest("form") },
    }));
  }

  body(m) {
    if (m.pending && m.code) {
      return html`<p class="app-muted">On the phone choose to link with a phone number, then enter:</p>
        <p class="app-code">${m.code}</p>`;
    }
    if (m.pending && m.qr) {
      return html`<img class="app-qr" src=${m.qr} alt="WhatsApp pairing QR code" width="256" height="256">
        <p class="app-muted">Scan with WhatsApp: Settings, Linked devices, Link a device.</p>`;
    }
    if (m.pending) return html`<p class="app-muted">Generating a QR code…</p>`;
    if (m.outcome === "connected" || m.state === "connected") return html`<p class="app-title">Paired ${m.phone}</p>`;
    if (m.outcome === "timeout") return html`<p class="app-muted">The pairing attempt expired. Run device_pair again.</p>`;
    if (m.outcome === "failed") return html`<p class="app-error">Pairing failed.</p>`;
    return html`<p class="app-muted">No pairing attempt is running.</p>`;
  }

  render() {
    const m = this.model;
    if (!m) return nothing;
    return html`<div class="app-card app-stack">
      ${this.body(m)}
      ${m.refresh ? html`<button class="app-action" @click=${(ev) => this.fire(m.refresh, ev)}>Refresh</button>` : nothing}
    </div>`;
  }
}

customElements.define("openwa-device-pair", DevicePairView);

registerView("device_pair", devicePairModel, (m) => html`<openwa-device-pair .model=${m}></openwa-device-pair>`);
registerView("device_get", deviceGetModel, (m) => html`<openwa-device-pair .model=${m}></openwa-device-pair>`);
