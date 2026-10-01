class ProjectListView extends LitElement {
  static properties = { model: { attribute: false } };
  static styles = appStyles;

  row(r) {
    return html`<div class="app-row">
      <span class="app-title">${r.name}</span>
      <span class="app-muted">${r.slug}</span>
      <span class="app-muted app-num">${r.id}</span>
    </div>`;
  }

  render() {
    const m = this.model;
    if (!m || m.empty) return html`<p class="app-muted">No projects yet.</p>`;
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

customElements.define("openwa-project-list", ProjectListView);

registerView("project_list", projectListModel, (m) => html`<openwa-project-list .model=${m}></openwa-project-list>`);
