class DeviceListView extends LitElement {
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

  row(r) {
    return html`<div class="app-row">
      <span class="app-badge" style="background:${r.badge}"></span>
      <span class="app-title">${r.name}</span>
      <span class="app-muted">${r.state}</span>
      <span class="app-muted">${r.phone}</span>
      <span class="app-muted app-num">${r.seen}</span>
      ${r.pair
        ? html`<button class="app-action" @click=${(ev) => this.fire(r.pair, ev)}>Pair</button>`
        : nothing}
    </div>`;
  }

  render() {
    const m = this.model;
    if (!m || m.empty) return html`<p class="app-muted">No devices in this project yet.</p>`;
    return html`<div class="app-stack">
      <div class="app-card app-kpis">
        ${m.kpis.map((k) => html`<div>
          <div class="app-kpi-label">${k.label}</div>
          <div class="app-kpi-value">${k.value}</div>
        </div>`)}
      </div>
      <div class="app-card">${m.rows.map((r) => this.row(r))}</div>
    </div>`;
  }
}

customElements.define("openwa-device-list", DeviceListView);

registerView("device_list", deviceListModel, (m) => html`<openwa-device-list .model=${m}></openwa-device-list>`);
