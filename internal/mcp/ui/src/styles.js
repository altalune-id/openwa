const appStyles = css`
  :host {
    display: block;
    padding: 12px;
    font-family: var(--font-sans, ui-sans-serif, system-ui, sans-serif);
    color: var(--color-text-primary, light-dark(#18181b, #f4f4f5));
  }
  .app-stack { display: flex; flex-direction: column; gap: 12px; }
  .app-muted { color: var(--color-text-secondary, light-dark(#52525b, #a1a1aa)); font-size: 0.875rem; }
  .app-error { color: var(--color-text-danger, light-dark(#b91c1c, #f87171)); }
  .app-card {
    border: 1px solid var(--color-border-primary, light-dark(#d4d4d8, #3f3f46));
    border-radius: 12px;
    padding: 12px;
    background: var(--color-background-secondary, light-dark(#f4f4f5, #232326));
  }
  .app-kpis { display: grid; grid-template-columns: repeat(auto-fit, minmax(110px, 1fr)); gap: 8px; }
  .app-kpi-label { font-size: 0.75rem; color: var(--color-text-secondary, light-dark(#52525b, #a1a1aa)); }
  .app-kpi-value { font-size: 1.05rem; font-weight: 600; }
  .app-row { display: flex; align-items: center; gap: 8px; padding: 4px 0; }
  .app-badge { width: 10px; height: 10px; border-radius: 50%; flex: none; }
  .app-title { font-weight: 500; }
  .app-num { font-variant-numeric: tabular-nums; }
  .app-row > .app-num { margin-left: auto; }
  button.app-action {
    font: inherit;
    cursor: pointer;
    border-radius: 8px;
    padding: 4px 10px;
    border: 1px solid var(--color-border-primary, light-dark(#d4d4d8, #3f3f46));
    background: var(--color-background-primary, light-dark(#fff, #18181b));
    color: inherit;
  }
  .app-qr { display: block; background: #fff; border-radius: 8px; padding: 8px; image-rendering: pixelated; }
  .app-code { font-family: ui-monospace, monospace; font-size: 1.5rem; font-weight: 600; letter-spacing: 0.2em; }
  .app-chat { padding: 8px 0; border-bottom: 1px solid var(--color-border-primary, light-dark(#d4d4d8, #3f3f46)); }
  .app-chat:last-child { border-bottom: none; }
  .app-clip { margin: 2px 0 6px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .app-inline { display: flex; gap: 6px; }
  .app-input { flex: 1; min-width: 0; font: inherit; padding: 4px 8px; border: 1px solid var(--color-border-primary, light-dark(#d4d4d8, #3f3f46)); border-radius: 6px; background: transparent; color: inherit; }
  .app-pill { min-width: 1.5em; padding: 0 6px; border-radius: 999px; background: var(--color-badge-success, #16a34a); color: #fff; font-size: 0.75rem; text-align: center; }
  .app-body { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
`;
