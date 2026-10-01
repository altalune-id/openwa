function projectRow(p) {
  return { id: text(p.id, ""), slug: text(p.slug, ""), name: text(p.name, "Untitled") };
}

function projectListModel(d) {
  const projects = d.projects || [];
  return {
    empty: projects.length === 0,
    kpis: [{ label: "Projects", value: num(projects.length) }],
    rows: projects.map(projectRow),
  };
}
