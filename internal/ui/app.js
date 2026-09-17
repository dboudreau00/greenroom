/* Greenroom portal. Plain JS, hash routing, no build step. */
(function () {
  'use strict';

  const app = document.getElementById('app');
  const crumbs = document.getElementById('crumbs');
  let me = { actor: '', auth: false, version: '' };
  let timer = null;
  let source = null;

  // ---------- helpers ----------
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  async function api(path, opts = {}) {
    const headers = Object.assign({ 'X-Requested-With': 'greenroom' }, opts.headers || {});
    if (opts.body && typeof opts.body !== 'string') {
      opts.body = JSON.stringify(opts.body);
      headers['Content-Type'] = 'application/json';
    }
    const res = await fetch(path, Object.assign({}, opts, { headers }));
    if (res.status === 401) { location.href = '/login'; throw new Error('signed out'); }
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch (e) { data = { error: text }; }
    if (!res.ok) throw new Error((data && data.error) || res.statusText);
    return data;
  }

  function toast(msg, kind) {
    const el = document.createElement('div');
    el.className = 'toast ' + (kind || '');
    el.textContent = msg;
    document.getElementById('toasts').appendChild(el);
    setTimeout(() => el.remove(), 6000);
  }

  function ago(iso) {
    if (!iso) return 'never';
    const t = new Date(iso).getTime();
    if (isNaN(t) || t < 0 || t === 0) return 'never';
    const s = Math.max(0, (Date.now() - t) / 1000);
    if (s < 60) return Math.floor(s) + 's ago';
    if (s < 3600) return Math.floor(s / 60) + 'm ago';
    if (s < 86400 * 2) return Math.floor(s / 3600) + 'h ' + Math.floor((s % 3600) / 60) + 'm ago';
    return Math.floor(s / 86400) + 'd ago';
  }
  function when(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (isNaN(d.getTime()) || d.getFullYear() < 2000) return '';
    return d.toLocaleString();
  }
  function dur(ns) {
    if (ns == null) return '';
    const ms = ns / 1e6;
    if (ms < 1000) return Math.round(ms) + 'ms';
    if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
    const m = Math.floor(ms / 60000), s = Math.round((ms % 60000) / 1000);
    return m + 'm' + (s ? ' ' + s + 's' : '');
  }
  function pct(v) { return v == null ? '' : (Math.round(v * 100) / 100).toFixed(2) + '%'; }
  function badge(kind, label) { return '<span class="badge ' + esc(kind) + '">' + esc(label) + '</span>'; }
  function statusBadge(st) {
    const map = { ok: 'green', failed: 'red', running: 'blue', cancelled: 'grey', skipped: 'grey' };
    return badge(map[st] || 'grey', st);
  }
  function readinessBadge(r, big) {
    const label = { green: 'GREEN', amber: 'AMBER', red: 'RED' }[r] || (r || '').toUpperCase();
    return '<span class="badge ' + esc(r) + (big ? ' big' : '') + '">' + esc(label) + '</span>';
  }
  function spark(points) {
    if (!points || !points.length) return '<span class="muted small">no data</span>';
    return '<span class="spark">' + points.map((p) => '<i class="' + (p === 1 ? 'ok' : p === 0 ? 'bad' : '') + '"></i>').join('') + '</span>';
  }
  function setCrumbs(parts) {
    crumbs.innerHTML = parts.map((p, i) => (p.href ? '<a href="' + esc(p.href) + '">' + esc(p.label) + '</a>' : '<span>' + esc(p.label) + '</span>') + (i < parts.length - 1 ? '<span class="muted">/</span>' : '')).join('');
  }
  function stopLive() {
    if (timer) { clearInterval(timer); timer = null; }
    if (source) { source.close(); source = null; }
  }

  // ---------- modal ----------
  function modal(html, onMount) {
    const root = document.getElementById('modal-root');
    root.innerHTML = '<div class="modal-back"><div class="modal" role="dialog">' + html + '</div></div>';
    const onKey = (e) => { if (e.key === 'Escape') close(); };
    const close = () => { root.innerHTML = ''; document.removeEventListener('keydown', onKey); };
    document.addEventListener('keydown', onKey);
    root.querySelector('.modal-back').addEventListener('click', (e) => { if (e.target.classList.contains('modal-back')) close(); });
    root.querySelectorAll('[data-close]').forEach((b) => b.addEventListener('click', close));
    if (onMount) onMount(root, close);
    return close;
  }

  // ---------- overview ----------
  async function renderOverview() {
    stopLive();
    setCrumbs([{ label: 'Environments' }]);
    const draw = async () => {
      const data = await api('/api/v1/overview');
      const envs = data.environments || [];
      let html = '<div class="page-head"><div><h1>Demo environments</h1><div class="sub">' + envs.length + ' environment' + (envs.length === 1 ? '' : 's') + ' in ' + esc(data.environmentsDir) + ', probed every ' + esc(data.probeInterval) + '</div></div></div>';
      if (data.loadError) html += '<div class="error-box"><b>Some environments failed to load.</b><br><span class="mono">' + esc(data.loadError) + '</span></div>';
      if (!envs.length) {
        html += '<div class="empty">No environments yet. Create <code>environments/&lt;name&gt;/environment.json</code> or run <code>greenroom init</code>.</div>';
      } else {
        html += '<div class="grid">' + envs.map((e) => envCard(e)).join('') + '</div>';
      }
      app.innerHTML = html;
    };
    await draw();
    timer = setInterval(() => draw().catch(() => {}), 30000);
  }

  function envCard(e) {
    const applied = e.applied ? esc(e.applied.setId) + ' <span class="muted">' + ago(e.applied.at) + '</span>' : '<span class="muted">never reset</span>';
    return '<a class="card link" href="#/env/' + esc(e.name) + '">' +
      '<h2>' + readinessBadge(e.readiness) + esc(e.title) + (e.busy ? ' <span class="pill on">busy</span>' : '') + '</h2>' +
      '<div class="muted small" style="margin-top:4px">' + esc(e.summary) + '</div>' +
      '<dl class="meta">' +
      '<dt>Pinned</dt><dd>' + esc(e.pinned || 'none') + '</dd>' +
      '<dt>Applied</dt><dd>' + applied + '</dd>' +
      '<dt>Dependencies</dt><dd>' + e.deps + (e.down ? ' <span class="badge red">' + e.down + ' down</span>' : '') + (e.warn ? ' <span class="badge amber">' + e.warn + ' warn</span>' : '') + '</dd>' +
      '<dt>Stale</dt><dd>' + (e.stale ? '<span class="badge amber">' + e.stale + ' item' + (e.stale === 1 ? '' : 's') + '</span>' : '<span class="badge green">fresh</span>') + '</dd>' +
      '<dt>Uptime 24h</dt><dd>' + (e.lastProbe ? pct(e.uptime24h) : '<span class="muted">no samples</span>') + '</dd>' +
      '<dt>Owner</dt><dd>' + esc(e.owner || '') + '</dd>' +
      '</dl></a>';
  }

  // ---------- environment ----------
  async function renderEnv(name, tab) {
    stopLive();
    tab = tab || 'deps';
    const draw = async (drift) => {
      const d = await api('/api/v1/environments/' + encodeURIComponent(name) + (drift ? '?drift=true' : ''));
      setCrumbs([{ label: 'Environments', href: '#/' }, { label: d.title }]);
      const tabs = [
        ['deps', 'Dependencies', d.deps], ['seeds', 'Seed sets', d.seeds.length], ['stale', 'Stale', d.stale ? d.stale.items.length : 0],
        ['runs', 'Runs', d.runs.length], ['targets', 'Targets', d.targets.length],
      ];
      let html = '<div class="page-head"><div><h1>' + readinessBadge(d.readiness, true) + ' ' + esc(d.title) + '</h1>' +
        '<div class="sub">' + esc(d.summary) + (d.lastProbe ? ' <span class="muted">(probed ' + ago(d.lastProbe) + ')</span>' : '') + '</div>' +
        (d.description ? '<div class="muted" style="margin-top:6px">' + esc(d.description) + '</div>' : '') + '</div>' +
        '<div class="actions">' +
        '<button class="btn" id="act-check">Pre-call check</button>' +
        '<button class="btn primary" id="act-reset"' + (d.busy ? ' disabled' : '') + '>Reset to known good</button>' +
        '</div></div>';
      if (d.busy) html += '<div class="error-box" style="border-color:var(--accent);background:rgba(110,168,254,.1)">A run is in progress: <a href="#/env/' + esc(name) + '/run/' + esc(d.busy) + '">' + esc(d.busy) + '</a></div>';
      if (d.problems && d.problems.length) html += '<div class="error-box">' + d.problems.map(esc).join('<br>') + '</div>';
      html += '<div class="stats">' +
        stat(d.pinned || 'none', 'pinned seed set') +
        stat(d.applied ? d.applied.setId : 'none', 'applied' + (d.applied ? ', ' + ago(d.applied.at) : '')) +
        stat(d.deps + (d.down ? ' <span class="badge red">' + d.down + ' down</span>' : ''), 'dependencies') +
        stat(d.lastProbe ? pct(d.uptime24h) : 'n/a', 'uptime 24h, all dependencies') +
        stat(d.stale ? d.stale.stale : 0, 'stale items (limit ' + esc(d.staleAfter) + ')') +
        '</div>';
      html += '<div class="tabs">' + tabs.map((t) => '<a href="#/env/' + esc(name) + '/' + t[0] + '" class="' + (t[0] === tab ? 'active' : '') + '">' + t[1] + '<span class="pill count">' + t[2] + '</span></a>').join('') + '</div>';
      switch (tab) {
        case 'seeds': html += seedsTab(d); break;
        case 'stale': html += staleTab(d, drift); break;
        case 'runs': html += runsTab(d); break;
        case 'targets': html += '<div id="targets-tab">' + targetsTab(d, null) + '</div>'; break;
        default: html += depsTab(d);
      }
      app.innerHTML = html;
      wireEnv(d, name, tab, draw);
      if (tab === 'targets') loadTargets(name, d);
    };
    await draw(false);
    timer = setInterval(() => draw(false).catch(() => {}), 30000);
  }

  function stat(v, label) { return '<div class="card stat"><b>' + v + '</b><span>' + esc(label) + '</span></div>'; }

  function depsTab(d) {
    if (!d.dependencies.length) return '<div class="empty">No dependencies.</div>';
    const rows = d.dependencies.map((dep) => {
      const l = dep.latest;
      const st = !l ? badge('grey', 'not probed') : !l.ok ? badge('red', 'down') : l.warn ? badge('amber', 'warn') : badge('green', 'up');
      const target = dep.address || dep.url || dep.container || dep.resource || dep.instanceId || (dep.dsn ? dep.dsn : '') || (dep.via ? 'via ' + dep.via.target : '') || (dep.command ? dep.command.join(' ') : '');
      const u = dep.uptime24h, u7 = dep.uptime7d;
      const inc = u.incidents.filter((i) => i.open).length;
      const detail = l && l.detail ? Object.entries(l.detail).filter(([k]) => ['version', 'status', 'image', 'ready', 'desired', 'state', 'daysLeft', 'health', 'privateIp', 'phase'].includes(k)).map(([k, v]) => k + '=' + (Array.isArray(v) ? v.join(',') : v)).join('  ') : '';
      return '<tr>' +
        '<td><div class="dep-name">' + esc(dep.title || dep.name) + (dep.critical ? ' <span class="pill">critical</span>' : '') + (dep.autoHeal ? ' <span class="pill">auto-heal</span>' : '') + '</div><div class="dep-kind">' + esc(dep.kind) + ' <span class="mono">' + esc(target) + '</span></div></td>' +
        '<td>' + st + (l ? '<div class="small muted" style="margin-top:3px">' + esc(l.message) + '</div>' : '') + (detail ? '<div class="small mono muted">' + esc(detail) + '</div>' : '') + '</td>' +
        '<td class="num">' + (l ? dur(l.latency) : '') + '</td>' +
        '<td>' + spark(u.points) + '<div class="small muted">' + (u.total ? pct(u.percent) + ' of ' + u.total + (inc ? ', <span style="color:var(--red)">incident open</span>' : '') : 'no samples') + '</div></td>' +
        '<td class="num">' + (u7.total ? pct(u7.percent) : '') + '</td>' +
        '<td><div class="actions">' +
        (dep.hasHeal ? '<button class="btn small" data-heal="' + esc(dep.name) + '">Heal (' + esc(dep.heal.action) + ')</button>' : '') +
        (dep.canSnapshot ? '<button class="btn small" data-snap="' + esc(dep.name) + '" title="' + (dep.lastSnapshot ? 'last snapshot ' + esc(ago(dep.lastSnapshot.at)) : 'no snapshot yet') + '">Snapshot</button>' : '') +
        '</div>' + (dep.hasReset ? '<div class="small muted">reset: ' + esc(dep.reset.strategy) + '</div>' : '') + '</td>' +
        '</tr>';
    }).join('');
    return '<div class="table-wrap"><table><thead><tr><th>Dependency</th><th>Status</th><th class="num">Latency</th><th>Uptime 24h</th><th class="num">7d</th><th>Actions</th></tr></thead><tbody>' + rows + '</tbody></table></div>' +
      (d.heals.length ? '<div class="section"><div class="section-head"><h2>Recent heals</h2></div><div class="table-wrap"><table><thead><tr><th>When</th><th>Dependency</th><th>Action</th><th>Trigger</th><th>Result</th></tr></thead><tbody>' +
        d.heals.map((h) => '<tr><td>' + esc(when(h.at)) + '</td><td>' + esc(h.dep) + '</td><td>' + esc(h.action) + '</td><td>' + (h.auto ? 'automatic' : 'manual') + '</td><td>' + (h.ok ? badge('green', 'ok') : badge('red', 'failed') + ' <span class="small muted">' + esc(h.error || '') + '</span>') + '</td></tr>').join('') +
        '</tbody></table></div></div>' : '');
  }

  function seedsTab(d) {
    if (!d.seeds.length) return '<div class="empty">No seed sets. Add <code>seeds/&lt;persona&gt;/v1/seed.json</code> under the environment directory.</div>';
    const personas = {};
    d.seeds.forEach((s) => {
      s.problems = s.problems || []; s.tags = s.tags || []; s.verify = s.verify || []; s.files = s.files || []; s.steps = s.steps || [];
      (personas[s.persona] = personas[s.persona] || []).push(s);
    });
    return Object.entries(personas).map(([p, sets]) => {
      return '<div class="section"><div class="section-head"><h2>Persona: ' + esc(p) + '</h2><span class="muted small">' + sets.length + ' version' + (sets.length === 1 ? '' : 's') + '</span></div>' +
        sets.slice().reverse().map((s) => '<div class="card" style="margin-bottom:10px"><h2>' + esc(s.id) + ' <span class="muted" style="font-weight:400">' + esc(s.title) + '</span>' +
          (s.pinned ? ' <span class="pill on">pinned</span>' : '') + (s.applied ? ' <span class="pill on">applied' + (s.changed ? ', files changed since' : '') + '</span>' : '') + (s.latest ? ' <span class="pill">latest</span>' : '') +
          '<span class="spacer"></span><button class="btn small" data-reset-persona="' + esc(s.persona) + '" data-reset-version="' + s.version + '">Apply</button></h2>' +
          (s.description ? '<div class="muted" style="margin:6px 0">' + esc(s.description) + '</div>' : '') +
          (s.problems.length ? '<div class="error-box">' + s.problems.map(esc).join('<br>') + '</div>' : '') +
          '<div class="two"><div><h3>Steps</h3><ol style="margin:6px 0 0 18px;padding:0">' + s.steps.map((st) => '<li><b>' + esc(st.name) + '</b> <span class="muted">' + esc(st.kind) + (st.dependency ? ' on ' + esc(st.dependency) : '') + (st.target ? ' via ' + esc(st.target) : '') + (st.file ? ' ' : '') + '</span>' + (st.file ? '<span class="mono">' + esc(st.file) + '</span>' : '') + '</li>').join('') + '</ol>' +
          (s.verify && s.verify.length ? '<h3 style="margin-top:8px">Verify</h3><ul style="margin:6px 0 0 18px;padding:0">' + s.verify.map((c) => '<li>' + esc(c.name) + ' <span class="muted">' + esc(c.kind) + '</span></li>').join('') + '</ul>' : '') +
          '</div><div><h3>Files</h3><ul class="files plain">' + s.files.map((f) => '<li class="mono">' + esc(f.path) + ' <span class="muted">' + f.size + ' B</span></li>').join('') + '</ul><div class="small muted mono" style="margin-top:6px">hash ' + esc(s.hash.slice(0, 16)) + '</div></div></div></div>').join('') + '</div>';
    }).join('');
  }

  function staleTab(d, drift) {
    const s = d.stale;
    let html = '<div class="section-head"><div>' + (s && s.fresh ? badge('green', 'fresh') : badge('amber', (s ? s.stale : 0) + ' stale')) + ' <span class="muted small">' + (drift ? 'drift queries included' : 'drift queries not run; use the button to include them') + '</span></div>' +
      '<button class="btn" id="act-stale">Check with drift queries</button></div>';
    if (!s || !s.items.length) return html + '<div class="empty">Nothing stale. The applied seed set matches the pin, the files, and the fingerprints.</div>';
    const sev = { stale: 'amber', warn: 'amber', info: 'blue' };
    html += '<div class="card">' + s.items.map((it) => '<div class="stale-item"><span class="sev">' + badge(it.severity === 'stale' ? 'red' : sev[it.severity], it.severity) + '</span><div>' + (it.dep ? '<b>' + esc(it.dep) + '</b> ' : '') + esc(it.message) + (it.fix ? '<div class="fix">' + esc(it.fix) + '</div>' : '') + '</div></div>').join('') + '</div>';
    if (s.applied) {
      html += '<div class="section"><h2>Applied record</h2><div class="card" style="margin-top:8px"><dl class="kv">' +
        '<dt>Seed set</dt><dd>' + esc(s.applied.setId) + '</dd><dt>Hash</dt><dd class="mono">' + esc(s.applied.setHash) + '</dd><dt>At</dt><dd>' + esc(when(s.applied.at)) + ' (' + ago(s.applied.at) + ')</dd><dt>Run</dt><dd><a href="#/env/' + esc(d.name) + '/run/' + esc(s.applied.runId) + '">' + esc(s.applied.runId) + '</a></dd><dt>By</dt><dd>' + esc(s.applied.actor || '') + '</dd>' +
        '<dt>Fingerprints</dt><dd>' + Object.entries(s.applied.fingerprints || {}).map(([k, f]) => '<div><b>' + esc(k) + '</b> <span class="mono">' + esc(f.kind + ' ' + f.value) + (f.detail ? ' (' + esc(f.detail) + ')' : '') + '</span></div>').join('') + '</dd>' +
        '<dt>Baselines</dt><dd>' + Object.entries(s.applied.baselines || {}).map(([k, m]) => Object.entries(m).map(([n, v]) => '<div><b>' + esc(k) + '</b>.' + esc(n) + ' = ' + esc(v) + '</div>').join('')).join('') + '</dd></dl></div></div>';
    }
    return html;
  }

  function runsTab(d) {
    if (!d.runs.length) return '<div class="empty">No runs yet.</div>';
    return '<div class="table-wrap"><table><thead><tr><th>Started</th><th>Kind</th><th>Status</th><th>Summary</th><th class="num">Duration</th><th>By</th></tr></thead><tbody>' +
      d.runs.map((r) => '<tr><td><a href="#/env/' + esc(d.name) + '/run/' + esc(r.id) + '">' + esc(when(r.startedAt)) + '</a><div class="small muted">' + ago(r.startedAt) + '</div></td><td>' + esc(r.kind) + (r.persona ? ' <span class="pill">' + esc(r.persona) + '@v' + r.version + '</span>' : '') + '</td><td>' + statusBadge(r.status) + (r.failed ? ' <span class="small muted">' + r.failed + ' failed</span>' : '') + '</td><td>' + esc(r.summary || r.error || '') + '</td><td class="num">' + dur(r.duration) + '</td><td>' + esc(r.actor || '') + '</td></tr>').join('') +
      '</tbody></table></div>';
  }

  function targetsTab(d, status) {
    if (!d.targets.length) return '<div class="empty">This environment declares no targets. Probes that need none (tcp, http, direct SQL) still work.</div>';
    return '<div class="table-wrap"><table><thead><tr><th>Target</th><th>Kind</th><th>Endpoint</th><th>Connectivity</th></tr></thead><tbody>' +
      d.targets.map((t) => {
        const s = status && status.find((x) => x.name === t.name);
        return '<tr><td class="dep-name">' + esc(t.name) + '</td><td>' + esc(t.kind) + '</td><td class="mono">' + esc(t.host || '') + '</td><td>' + (!status ? '<span class="muted">checking</span>' : s.ok ? badge('green', 'reachable') + ' <span class="small muted">' + esc(s.message) + ' (' + dur(s.latency) + ')</span>' : badge('red', 'unreachable') + ' <span class="small muted">' + esc(s.message) + '</span>') + '</td></tr>';
      }).join('') + '</tbody></table></div>';
  }

  async function loadTargets(name, d) {
    try {
      const st = await api('/api/v1/environments/' + encodeURIComponent(name) + '/targets');
      const el = document.getElementById('targets-tab');
      if (el) el.innerHTML = targetsTab(d, st);
    } catch (e) { toast(e.message, 'error'); }
  }

  function wireEnv(d, name, tab, draw) {
    const check = document.getElementById('act-check');
    if (check) check.addEventListener('click', async () => {
      check.disabled = true; check.textContent = 'Checking';
      try {
        const rep = await api('/api/v1/environments/' + encodeURIComponent(name) + '/check?record=true&drift=true', { method: 'POST' });
        showCheck(name, rep);
        draw(false).catch(() => {});
      } catch (e) { toast(e.message, 'error'); }
      check.disabled = false; check.textContent = 'Pre-call check';
    });
    const reset = document.getElementById('act-reset');
    if (reset) reset.addEventListener('click', () => resetDialog(d, name));
    app.querySelectorAll('[data-reset-persona]').forEach((b) => b.addEventListener('click', () => resetDialog(d, name, b.dataset.resetPersona, parseInt(b.dataset.resetVersion, 10))));
    app.querySelectorAll('[data-heal]').forEach((b) => b.addEventListener('click', async () => {
      b.disabled = true;
      try {
        const r = await api('/api/v1/environments/' + encodeURIComponent(name) + '/dependencies/' + encodeURIComponent(b.dataset.heal) + '/heal', { method: 'POST' });
        location.hash = '#/env/' + name + '/run/' + r.runId;
      } catch (e) { toast(e.message, 'error'); b.disabled = false; }
    }));
    app.querySelectorAll('[data-snap]').forEach((b) => b.addEventListener('click', () => {
      const dep = b.dataset.snap;
      modal('<h2>Snapshot ' + esc(dep) + '</h2><p class="muted">Captures the current state of this dependency as the known-good baseline the reset strategy restores. For Postgres this clones the database into a template (sessions are terminated); for Docker it commits the container and archives its mounts (the container is stopped briefly).</p><div class="foot"><button class="btn" data-close>Cancel</button><button class="btn primary" id="snap-go">Capture snapshot</button></div>', (root, close) => {
        root.querySelector('#snap-go').addEventListener('click', async () => {
          try {
            const r = await api('/api/v1/environments/' + encodeURIComponent(name) + '/snapshot', { method: 'POST', body: { dependency: dep } });
            close();
            location.hash = '#/env/' + name + '/run/' + r.runId;
          } catch (e) { toast(e.message, 'error'); }
        });
      });
    }));
    const stale = document.getElementById('act-stale');
    if (stale) stale.addEventListener('click', async () => { stale.disabled = true; stale.textContent = 'Running drift queries'; try { await draw(true); } catch (e) { toast(e.message, 'error'); } });
  }

  function showCheck(name, rep) {
    const rows = rep.results.map((r) => '<tr><td><b>' + esc(r.dep) + '</b>' + (r.critical ? ' <span class="pill">critical</span>' : '') + '</td><td>' + (!r.ok ? badge('red', 'down') : r.warn ? badge('amber', 'warn') : badge('green', 'up')) + '</td><td class="small">' + esc(r.message) + '</td><td class="num small">' + dur(r.latency) + '</td></tr>').join('');
    const staleRows = rep.stale && rep.stale.items.length ? '<h3 style="margin-top:12px">Stale</h3>' + rep.stale.items.map((it) => '<div class="stale-item"><span class="sev">' + badge(it.severity === 'stale' ? 'red' : it.severity === 'warn' ? 'amber' : 'blue', it.severity) + '</span><div>' + (it.dep ? '<b>' + esc(it.dep) + '</b> ' : '') + esc(it.message) + '</div></div>').join('') : '<div class="muted small" style="margin-top:10px">Nothing stale.</div>';
    modal('<h2>' + readinessBadge(rep.readiness, true) + ' Pre-call check</h2><div class="muted">' + esc(rep.summary) + ' <span class="small">(' + dur(rep.duration) + ')</span></div>' +
      '<div class="table-wrap" style="margin-top:12px;max-height:40vh;overflow:auto"><table><tbody>' + rows + '</tbody></table></div>' + staleRows +
      '<div class="foot"><a class="btn ghost" href="#/env/' + esc(name) + '/run/' + esc(rep.runId) + '" data-close>Open run</a><button class="btn primary" data-close>Close</button></div>');
  }

  function resetDialog(d, name, persona, version) {
    const personas = {};
    d.seeds.forEach((s) => { (personas[s.persona] = personas[s.persona] || []).push(s); });
    const names = Object.keys(personas);
    if (!names.length) { toast('This environment has no seed sets', 'error'); return; }
    let pinnedPersona = persona || (d.pinned ? d.pinned.split('@')[0] : names[0]);
    if (!personas[pinnedPersona]) pinnedPersona = names[0];
    const options = () => names.map((p) => '<option value="' + esc(p) + '"' + (p === pinnedPersona ? ' selected' : '') + '>' + esc(p) + '</option>').join('');
    const versions = (p, v) => personas[p].slice().reverse().map((s) => '<option value="' + s.version + '"' + ((v ? s.version === v : s.pinned) ? ' selected' : '') + '>v' + s.version + ' ' + esc(s.title) + (s.pinned ? ' (pinned)' : '') + (s.applied ? ' (applied)' : '') + ((s.problems || []).length ? ' (has problems)' : '') + '</option>').join('');
    modal('<h2>Reset ' + esc(d.title) + '</h2><p class="muted">Runs the reset strategy of every dependency that has one, applies the seed set, verifies, then records fingerprints. Anything demoed since the last reset is discarded.</p>' +
      '<div class="row"><label>Persona</label><select id="rs-persona">' + options() + '</select></div>' +
      '<div class="row"><label>Version</label><select id="rs-version">' + versions(pinnedPersona, version) + '</select></div>' +
      '<label class="check"><input type="checkbox" id="rs-force"> Continue even if the preflight finds dependencies down</label>' +
      '<label class="check"><input type="checkbox" id="rs-skip"> Skip reset strategies (apply seed steps only)</label>' +
      '<div class="foot"><button class="btn" data-close>Cancel</button><button class="btn danger" id="rs-go">Reset now</button></div>', (root, close) => {
        const ps = root.querySelector('#rs-persona'), vs = root.querySelector('#rs-version');
        ps.addEventListener('change', () => { vs.innerHTML = versions(ps.value, 0); });
        root.querySelector('#rs-go').addEventListener('click', async () => {
          try {
            const r = await api('/api/v1/environments/' + encodeURIComponent(name) + '/reset', { method: 'POST', body: { persona: ps.value, version: parseInt(vs.value, 10), force: root.querySelector('#rs-force').checked, skipReset: root.querySelector('#rs-skip').checked } });
            close();
            location.hash = '#/env/' + name + '/run/' + r.runId;
          } catch (e) { toast(e.message, 'error'); }
        });
      });
  }

  // ---------- run viewer ----------
  async function renderRun(name, id) {
    stopLive();
    setCrumbs([{ label: 'Environments', href: '#/' }, { label: name, href: '#/env/' + name }, { label: 'Runs', href: '#/env/' + name + '/runs' }, { label: id }]);
    app.innerHTML = '<div class="empty">Loading run</div>';
    let run = null;
    const logEl = () => document.getElementById('log');
    const draw = () => {
      const r = run;
      app.innerHTML = '<div class="run-head"><h1>' + esc(r.kind) + (r.persona ? ' ' + esc(r.persona) + '@v' + r.version : '') + '</h1>' + statusBadge(r.status) +
        '<span class="muted">' + esc(when(r.startedAt)) + (r.actor ? ' by ' + esc(r.actor) : '') + (r.status !== 'running' ? ', took ' + dur(r.duration) : '') + '</span><span class="spacer"></span><span class="muted mono small">' + esc(r.id) + '</span></div>' +
        (r.summary ? '<div class="card" style="margin-bottom:12px"><b>' + esc(r.summary) + '</b></div>' : '') +
        (r.error && !r.summary ? '<div class="error-box">' + esc(r.error) + '</div>' : '') +
        '<div class="two"><div><h2 style="margin-bottom:8px">Steps</h2><div class="steps" id="steps">' + r.steps.map(stepHtml).join('') + (r.steps.length ? '' : '<div class="muted small">no steps yet</div>') + '</div></div>' +
        '<div><h2 style="margin-bottom:8px">Log <span class="muted small">' + r.log.length + ' lines</span></h2><div class="log" id="log">' + r.log.map(lineHtml).join('') + '</div></div></div>';
      const l = logEl(); if (l) l.scrollTop = l.scrollHeight;
    };
    const es = new EventSource('/api/v1/environments/' + encodeURIComponent(name) + '/runs/' + encodeURIComponent(id) + '/events');
    source = es;
    es.addEventListener('snapshot', (e) => { run = JSON.parse(e.data); draw(); });
    es.addEventListener('log', (e) => {
      const ev = JSON.parse(e.data); if (!run) return;
      run.log.push(ev.line);
      const l = logEl(); if (l) { l.insertAdjacentHTML('beforeend', lineHtml(ev.line)); l.scrollTop = l.scrollHeight; }
    });
    es.addEventListener('step', (e) => {
      const ev = JSON.parse(e.data); if (!run) return;
      const i = run.steps.findIndex((s) => s.name === ev.step.name && s.kind === ev.step.kind);
      if (i >= 0) run.steps[i] = ev.step; else run.steps.push(ev.step);
      const el = document.getElementById('steps'); if (el) el.innerHTML = run.steps.map(stepHtml).join('');
    });
    es.addEventListener('done', async (e) => {
      es.close(); source = null;
      try { run = await api('/api/v1/environments/' + encodeURIComponent(name) + '/runs/' + encodeURIComponent(id)); draw(); } catch (err) { /* keep what we have */ }
    });
    es.onerror = () => { if (run && run.status !== 'running') { es.close(); source = null; } };
  }
  function stepHtml(s) {
    return '<div class="step ' + esc(s.status) + '"><span class="ico"></span><div><b>' + esc(s.name) + '</b> <span class="muted small">' + esc(s.kind) + '</span>' + (s.error ? '<div class="err">' + esc(s.error) + '</div>' : '') + (s.detail ? '<div class="detail">' + esc(s.detail) + '</div>' : '') + '</div><span class="muted small">' + (s.status === 'running' ? '' : dur(s.duration)) + '</span></div>';
  }
  function lineHtml(l) {
    const t = new Date(l.at);
    const ts = isNaN(t.getTime()) ? '' : t.toTimeString().slice(0, 8);
    return '<div class="l"><span class="t">' + ts + '</span><span class="' + esc(l.level) + '">' + (l.step ? '<span class="step-tag">[' + esc(l.step) + ']</span>' : '') + esc(l.text) + '</span></div>';
  }

  // ---------- router ----------
  function route() {
    const h = location.hash.replace(/^#\/?/, '');
    const parts = h.split('/').filter(Boolean).map(decodeURIComponent);
    const go = (p) => p.catch((e) => { app.innerHTML = '<div class="error-box">' + esc(e.message) + '</div>'; });
    if (parts[0] === 'env' && parts[1]) {
      if (parts[2] === 'run' && parts[3]) return go(renderRun(parts[1], parts[3]));
      return go(renderEnv(parts[1], parts[2]));
    }
    return go(renderOverview());
  }
  window.addEventListener('hashchange', route);
  document.getElementById('btn-reload').addEventListener('click', async () => {
    try { const r = await api('/api/v1/reload', { method: 'POST' }); toast(r.ok ? 'Configuration reloaded: ' + r.environments + ' environments' : 'Reload finished with errors: ' + r.error, r.ok ? 'ok' : 'error'); route(); } catch (e) { toast(e.message, 'error'); }
  });
  setInterval(() => { document.getElementById('clock').textContent = new Date().toLocaleTimeString(); }, 1000);
  api('/api/v1/me').then((m) => { me = m; if (m.auth) document.getElementById('logout-form').hidden = false; }).catch(() => {});
  route();
})();
