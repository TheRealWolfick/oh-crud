// Oh CRUD management dashboard. Talks only to this server's /api, which forwards to
// Oh CRUD's /admin/v1 with the signed-in person's tokens.
(() => {
  'use strict';

  const csrf = document.querySelector('meta[name="csrf"]').content;
  const $ = (id) => document.getElementById(id);
  const view = $('view'), drawerEl = $('drawer'), dialog = $('dialog'), toastsEl = $('toasts');

  // ── Safety and formatting ───────────────────────────────────────────────
  const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const enc = encodeURIComponent;
  const IDLE_DAYS = 30;

  function when(iso) {
    if (!iso) return 'Never';
    const d = new Date(iso), now = new Date(), secs = (now - d) / 1000;
    if (secs < 60) return Math.max(1, Math.round(secs)) + ' s ago';
    if (secs < 3600) return Math.round(secs / 60) + ' min ago';
    const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
    if (d.toDateString() === now.toDateString()) return 'Today ' + time;
    const y = new Date(now); y.setDate(now.getDate() - 1);
    if (d.toDateString() === y.toDateString()) return 'Yesterday ' + time;
    return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric' });
  }
  const full = (iso) => iso ? new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }) : '—';
  const ageDays = (iso) => iso ? (Date.now() - new Date(iso)) / 86400000 : Infinity;
  // How fresh a service's last signature is, as a bar: full within a day, empty at idle
  const freshness = (iso) => { const d = ageDays(iso); return d === Infinity ? 0 : d <= 1 ? 100 : Math.max(4, Math.round(100 - (d / IDLE_DAYS) * 100)); };
  const initials = (name) => String(name || '?').split(/[\s._-]+/).filter(Boolean).slice(0, 2).map((p) => p[0].toUpperCase()).join('') || '?';
  const firstName = (name) => String(name || '').split(/[\s.]/)[0];
  const plural = (n, one, many) => n + ' ' + (n === 1 ? one : (many || one + 's'));

  const statusBadge = {
    active: ['b-ok', 'Active'], idle: ['b-warn', 'Idle'], disabled: ['b-bad', 'Disabled'],
    sessions_ended: ['b-warn', 'Sessions ended'], service: ['b-mute', 'Service'],
    live: ['b-ok', 'Live'], revoked: ['b-bad', 'Ended'], expired: ['b-mute', 'Expired'],
  };
  const badge = (status) => { const [cls, label] = statusBadge[status] || ['b-mute', status]; return `<span class="badge ${cls}">${esc(label)}</span>`; };
  const bar = (pct, tone) => `<span class="track ${tone || ''}"><span data-pct="${pct}"></span></span>`;
  const chevron = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="m9 6 6 6-6 6"></path></svg>';
  const closeIcon = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18"></path></svg>';

  // ── Audit entries, in words ─────────────────────────────────────────────
  const actionWords = {
    'user.login': ['Signed in', 'b-mute'], 'user.login_failed': ['Sign-in failed', 'b-warn'], 'user.logout': ['Signed out', 'b-mute'],
    'user.logout_all': ['Signed out everywhere', 'b-mute'], 'refresh_token.reuse': ['Token reuse', 'b-warn'],
    'refresh_token.wrong_client': ['Token misuse', 'b-warn'], 'session.revoke': ['Session ended', 'b-warn'],
    'service.create': ['Service added', 'b-calm'], 'service.update': ['Role', 'b-calm'], 'service.disable': ['Disabled', 'b-bad'],
    'service.enable': ['Enabled', 'b-ok'], 'service_key.add': ['Key added', 'b-calm'], 'service_key.revoke': ['Key revoked', 'b-bad'],
    'user.create': ['User added', 'b-calm'], 'user.roles.update': ['Roles', 'b-calm'], 'user.disable': ['Disabled', 'b-bad'],
    'user.enable': ['Enabled', 'b-ok'], 'user.sessions_revoke': ['Sessions ended', 'b-warn'], 'user.session_revoke': ['Session ended', 'b-warn'],
    'schema.approve': ['Schema', 'b-mute'], 'schema.deny': ['Schema', 'b-mute'],
  };
  const kindOf = (a) => actionWords[a] || [a, 'b-mute'];
  const people = new Map(); // user id -> username, for showing actors by name
  const nameOf = (id) => !id ? 'system' : people.get(id) || id;
  function describe(e) {
    const t = e.target.startsWith('username:') ? e.target.slice(9) : nameOf(e.target);
    const after = e.after || {}, before = e.before || {};
    switch (e.action) {
      case 'user.login': return `${t} signed in`;
      case 'user.login_failed': return `Sign-in failed for ${t}` + (after.reason ? ` (${after.reason.replace(/_/g, ' ')})` : '');
      case 'user.logout': return `${t} signed out`;
      case 'user.logout_all': return `${t} signed out of every session`;
      case 'refresh_token.reuse': return `Ended a session of ${t} after a refresh token was reused`;
      case 'refresh_token.wrong_client': return `A refresh token of ${t} was presented by the wrong service`;
      case 'session.revoke': return `Ended ${t}'s sessions (${(after.reason || '').replace(/_/g, ' ')})`;
      case 'service.create': return `Registered ${e.target}`;
      case 'service.update': return `Changed ${e.target} from ${before.role} to ${after.role}`;
      case 'service.disable': return `Disabled ${e.target}`;
      case 'service.enable': return `Enabled ${e.target}`;
      case 'service_key.add': return `Added key ${e.target}`;
      case 'service_key.revoke': return `Revoked key ${e.target}`;
      case 'user.create': return `Added ${after.username || t}`;
      case 'user.roles.update': return `Changed ${t}'s roles to ${(after.roles || []).join(', ') || 'none'}`;
      case 'user.disable': return `Disabled ${t}`;
      case 'user.enable': return `Enabled ${t}`;
      case 'user.sessions_revoke': return `Ended every session of ${t}`;
      case 'user.session_revoke': return `Ended a session of ${t}`;
      case 'schema.approve': return `Approved a schema change on ${e.target}`;
      case 'schema.deny': return `Denied a schema change on ${e.target}`;
      default: return `${e.action} on ${t}`;
    }
  }
  async function loadPeople() {
    try {
      const r = await api('GET', '/users?limit=200');
      for (const u of r.items) people.set(u.id, u.display_name || u.username);
    } catch (_) { /* names are a nicety: ids still show */ }
  }

  // ── API ─────────────────────────────────────────────────────────────────
  class Problem extends Error {
    constructor(p, status, requestID, challenge) {
      super(p.title || 'Something went wrong');
      this.title = p.title || 'Something went wrong';
      this.detail = p.detail || '';
      this.status = status;
      this.requestID = requestID || p.request_id || '';
      this.stepUp = (challenge || '').includes('insufficient_user_authentication');
    }
  }

  async function api(method, path, body) {
    const opts = { method, headers: {}, credentials: 'same-origin' };
    if (method !== 'GET') opts.headers['X-CSRF-Token'] = csrf;
    if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    const res = await fetch('/api' + path, opts);
    let data = null;
    try { data = await res.json(); } catch (_) { /* empty or not JSON */ }
    const challenge = res.headers.get('WWW-Authenticate') || '';
    if (res.status === 401 && !challenge.includes('insufficient_user_authentication')) {
      location.href = '/login';
      throw new Problem({ title: 'Signed out' }, 401);
    }
    if (!res.ok) throw new Problem(data || { title: res.statusText }, res.status, res.headers.get('X-Request-Id'), challenge);
    return data;
  }

  function toast(title, detail, bad, requestID) {
    const el = document.createElement('div');
    el.className = 'toast' + (bad ? ' bad' : '');
    el.innerHTML = `<b>${esc(title)}</b>${detail ? esc(detail) : ''}${requestID ? `<small>Request ${esc(requestID)}</small>` : ''}`;
    toastsEl.append(el);
    setTimeout(() => el.remove(), bad ? 9000 : 4500);
  }
  function report(err) {
    if (err instanceof Problem) {
      if (err.stepUp) toast('A recent one-time code is needed', 'Sign out, sign in again with your code, then try once more.', true);
      else toast(err.title, err.detail, true, err.requestID);
    } else {
      toast('Something went wrong', String(err && err.message || err), true);
    }
  }

  // ── State and navigation ────────────────────────────────────────────────
  const state = {
    tab: 'overview',
    services: { q: '', stack: [''] },
    users: { status: 'all', q: '', stack: [''] },
    audit: { f: { actor: '', action: '', target: '', from: '', to: '', request_id: '' }, stack: [''], items: [] },
    recent: [],
    selected: null,
  };

  function setTab(tab) {
    state.tab = tab;
    if (location.hash !== '#' + tab) history.replaceState(null, '', '#' + tab);
    document.querySelectorAll('.nav').forEach((b) => { if (b.dataset.tab === tab) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current'); });
    render();
  }

  async function render() {
    try {
      if (state.tab === 'services') await renderServices();
      else if (state.tab === 'users') await renderUsers();
      else if (state.tab === 'audit') await renderAudit();
      else await renderOverview();
      paintBars(view);
    } catch (err) {
      if (err.title !== 'Signed out') { view.innerHTML = `<div class="card empty">Couldn't load this page.</div>`; report(err); }
    }
  }
  const paintBars = (root) => root.querySelectorAll('[data-pct]').forEach((el) => { el.style.width = el.dataset.pct + '%'; });

  const pager = (s, page, label) => `
    <div class="pager">
      <span class="muted">${esc(label)}</span>
      <div>
        <button class="btn btn-sm" type="button" data-act="page-prev" ${s.stack.length > 1 ? '' : 'disabled'}>Previous</button>
        <button class="btn btn-sm" type="button" data-act="page-next" data-cursor="${esc(page.next_cursor)}" ${page.next_cursor ? '' : 'disabled'}>Next</button>
      </div>
    </div>`;
  const cursorOf = (s) => s.stack[s.stack.length - 1];

  // ── Overview ────────────────────────────────────────────────────────────
  async function renderOverview() {
    const o = await api('GET', '/overview');
    state.recent = o.recent_changes;
    const t = o.tiles, needs = o.needs_you;
    const me = firstName($('me-name').textContent);
    const adminNote = t.admin_changes_today.total === 0 ? 'None yet'
      : t.admin_changes_today.with_otp === t.admin_changes_today.total ? 'All with a one-time code'
      : `${t.admin_changes_today.with_otp} with a one-time code`;
    view.innerHTML = `
      <div class="head-text">
        <h1>Welcome back, ${esc(me)}</h1>
        <p class="sub">${needs.length ? `${plural(needs.length, 'thing needs', 'things need')} you today.` : 'Nothing needs you today.'}</p>
      </div>
      <div class="tiles">
        <div class="tile dark"><span class="tile-label">Services</span>
          <div class="tile-foot"><span class="tile-num">${t.services.total}</span><span class="tile-note">${t.services.idle} idle${t.services.disabled ? `, ${t.services.disabled} disabled` : ''}</span></div></div>
        <div class="tile"><span class="tile-label muted">People</span>
          <div class="tile-foot"><span class="tile-num">${t.people.total}</span><span class="tile-note">${t.people.signed_in_today} signed in today</span></div></div>
        <div class="tile"><span class="tile-label muted">Sessions ended today</span>
          <div class="tile-foot"><span class="tile-num">${t.sessions_ended_today.total}</span><span class="tile-note ${t.sessions_ended_today.token_reuse ? 'alert' : ''}">${plural(t.sessions_ended_today.token_reuse, 'token reuse', 'token reuses')}</span></div></div>
        <div class="tile ember"><span class="tile-label">Admin changes today</span>
          <div class="tile-foot"><span class="tile-num">${t.admin_changes_today.total}</span><span class="tile-note">${esc(adminNote)}</span></div></div>
      </div>
      <div class="split">
        <section class="card wide" aria-label="Recent changes">
          <div class="card-head"><h2 class="card-title">Recent changes</h2><button class="btn btn-sm" type="button" data-act="tab" data-tab="audit">Open audit log</button></div>
          ${auditTable(o.recent_changes, 'recent', true)}
        </section>
        <div class="narrow">
          <section class="card card-pad" aria-label="Needs you">
            <h2 class="card-title">Needs you</h2>
            ${needs.length ? needs.map((n) => `
              <button class="link-btn" type="button" data-act="open-${esc(n.kind)}" data-id="${esc(n.id)}">
                <span class="dot ${n.kind === 'schema' ? 'schema' : ''}"></span><span>${esc(n.title)}</span>
              </button>`).join('') : '<span class="muted">All clear.</span>'}
          </section>
          <section class="card card-pad" aria-label="Key health">
            <h2 class="card-title">Key health</h2>
            ${o.key_health.length ? o.key_health.map((k) => `
              <button class="link-btn health" type="button" data-act="open-service" data-id="${esc(k.service)}">
                <span>${esc(k.service)}</span>${bar(freshness(k.last_signed_at), k.status === 'idle' ? 'warn' : k.status === 'disabled' ? 'bad' : '')}
                <span class="health-when ${k.status === 'idle' ? 'warn' : ''}">${esc(when(k.last_signed_at))}</span>
              </button>`).join('') : '<span class="muted">No services yet.</span>'}
          </section>
        </div>
      </div>`;
  }

  function auditTable(items, source, short) {
    if (!items.length) return '<div class="empty">Nothing recorded yet.</div>';
    return `<div class="table-wrap"><table>
      <thead><tr><th class="th th-open"><span class="sr">Details</span></th><th class="th">Time</th><th class="th">${short ? 'Who' : 'Actor'}</th>
        ${short ? '<th class="th">What changed</th><th class="th">Kind</th>' : '<th class="th">Via</th><th class="th">Action</th><th class="th">Target</th><th class="th">Request</th>'}</tr></thead>
      <tbody>${items.map((e) => {
        const [kind, cls] = kindOf(e.action);
        return `<tr class="row" data-act="open-event" data-source="${source}" data-id="${e.id}">
          <td class="td"><button class="open-btn" type="button" data-act="open-event" data-source="${source}" data-id="${e.id}" aria-label="Open details for request ${esc(e.request_id)}">${chevron}</button></td>
          <td class="td muted nowrap">${esc(when(e.at))}</td>
          <td class="td">${esc(nameOf(e.actor_user))}</td>
          ${short
            ? `<td class="td">${esc(describe(e))}</td><td class="td"><span class="badge ${cls}">${esc(kind)}</span></td>`
            : `<td class="td mono muted">${esc(e.via_service ? 'svc:' + e.via_service : '—')}</td><td class="td"><span class="badge ${cls}">${esc(e.action)}</span></td>
               <td class="td mono">${esc(e.target)}</td><td class="td mono muted">${esc((e.request_id || '').slice(0, 10))}</td>`}
        </tr>`; }).join('')}
      </tbody></table></div>`;
  }

  // ── Services ────────────────────────────────────────────────────────────
  async function renderServices() {
    const s = state.services;
    const page = await api('GET', `/services?limit=25&q=${enc(s.q)}&cursor=${enc(cursorOf(s))}`);
    view.innerHTML = `
      <div class="head">
        <div class="head-text"><h1>Services</h1>
          <p class="sub">Every client that calls Oh CRUD. Each holds its own Ed25519 key; Oh CRUD stores only the public half.</p></div>
        <button class="btn btn-primary" type="button" data-act="new-service">Register service</button>
      </div>
      <section class="card" aria-label="All services">
        <div class="card-head"><h2 class="card-title">All services</h2>
          <form data-form="service-search"><label><span class="sr">Search services</span><input class="field" name="q" type="search" placeholder="Search services" value="${esc(s.q)}"></label></form></div>
        ${page.items.length ? `<div class="table-wrap"><table>
          <thead><tr><th class="th th-open"><span class="sr">Details</span></th><th class="th">Service</th><th class="th">Role</th><th class="th">Keys</th><th class="th">Key last signed</th><th class="th">Writes recorded as</th><th class="th">Status</th></tr></thead>
          <tbody>${page.items.map((v) => `
            <tr class="row ${state.selected === 'service:' + v.id ? 'selected' : ''}" data-act="open-service" data-id="${esc(v.id)}">
              <td class="td"><button class="open-btn" type="button" data-act="open-service" data-id="${esc(v.id)}" aria-label="Open ${esc(v.id)} details">${chevron}</button></td>
              <td class="td strong">${esc(v.id)}</td>
              <td class="td"><span class="badge b-calm">${esc(v.role)}</span></td>
              <td class="td muted">${v.active_keys} active${v.revoked_keys ? `, ${v.revoked_keys} revoked` : ''}</td>
              <td class="td"><div class="signed">${bar(freshness(v.last_signed_at), v.status === 'idle' ? 'warn' : v.status === 'disabled' ? 'bad' : '')}<span class="nowrap">${esc(when(v.last_signed_at))}</span></div></td>
              <td class="td mono muted">${esc(v.writes_recorded_as)}</td>
              <td class="td">${badge(v.status)}</td>
            </tr>`).join('')}</tbody></table></div>` : '<div class="empty">No services match.</div>'}
        ${pager(s, page, `Page ${s.stack.length}, ${plural(page.items.length, 'service')}`)}
      </section>`;
  }

  async function openService(id) {
    state.selected = 'service:' + id;
    const r = await api('GET', `/services/${enc(id)}`);
    const v = r.service;
    const reach = [...r.reach.models.map((m) => `${m.methods.join(', ') || 'admin'} ${m.path}`), ...r.reach.functions.map((f) => `run ${f.path}`)];
    if (r.reach.platform_admin) reach.unshift('Everything: the platform admin role');
    openDrawer({
      kind: 'Service', title: v.id, sub: v.writes_recorded_as, status: v.status,
      note: v.status === 'idle' ? (v.last_signed_at ? `No request signed in ${Math.floor(ageDays(v.last_signed_at))} days. Revoke its key if the service is gone.` : 'This service has never signed a request.')
        : v.status === 'disabled' ? `Disabled ${full(v.disabled_at)}. It can't get tokens until it's enabled.` : '',
      facts: [['Role', v.role], ['Keys', `${v.active_keys} active, ${v.revoked_keys} revoked`], ['Key last signed', when(v.last_signed_at)], ['Writes recorded as', v.writes_recorded_as]],
      sections: [
        `<div class="section"><h3>What this role can reach</h3><div class="badges">${reach.length ? reach.map((x) => `<span class="badge b-calm">${esc(x)}</span>`).join('') : '<span class="muted">Nothing yet: no model lists this role.</span>'}</div></div>`,
        `<div class="section"><h3>Keys</h3>${r.keys.length ? r.keys.map((k) => `
          <div class="item"><div class="item-row">
            <div class="item-text"><b class="mono">${esc(k.kid)}</b><span>${k.revoked_at ? `Revoked ${esc(full(k.revoked_at))}${k.revoked_reason ? `: ${esc(k.revoked_reason)}` : ''}` : `Added ${esc(full(k.created_at))}, last signed ${esc(when(k.last_signed_at))}`}</span></div>
            ${k.revoked_at ? badge('revoked') : `<button class="btn btn-sm btn-danger" type="button" data-act="revoke-key" data-id="${esc(v.id)}" data-kid="${esc(k.kid)}">Revoke</button>`}
          </div>${k.revoked_at ? '' : bar(freshness(k.last_signed_at), ageDays(k.last_signed_at) > IDLE_DAYS ? 'warn' : '')}</div>`).join('') : '<span class="muted">No keys: it can\'t sign in.</span>'}</div>`,
      ],
      actions: [
        ['Add key', 'btn-soft', 'add-key', v.id], ['Change role', '', 'service-role', v.id],
        v.status === 'disabled' ? ['Enable service', 'btn-primary', 'service-enable', v.id] : ['Disable service', 'btn-danger', 'service-disable', v.id],
      ],
    });
  }

  // ── Users ───────────────────────────────────────────────────────────────
  const tabs = [['all', 'All users'], ['active', 'Active'], ['sessions_ended', 'Sessions ended'], ['disabled', 'Disabled'], ['service', 'Service rows']];
  function signIn(u) {
    if (u.is_service) return ['Cannot sign in', 0];
    if (u.has_password && u.has_otp) return ['Password and code', 2];
    if (u.has_password) return ['Password only', 1];
    return ['No password', 0];
  }
  async function renderUsers() {
    const s = state.users;
    const page = await api('GET', `/users?limit=25&status=${enc(s.status)}&q=${enc(s.q)}&cursor=${enc(cursorOf(s))}`);
    for (const u of page.items) people.set(u.id, u.display_name || u.username);
    const label = tabs.find((t) => t[0] === s.status)[1];
    view.innerHTML = `
      <div class="head">
        <div class="head-text"><h1>Users</h1>
          <p class="sub">People and service rows. Roles are matched against each model's allowed end points.</p></div>
        <button class="btn btn-primary" type="button" data-act="new-user">Create user</button>
      </div>
      <div class="toolbar">
        <div class="chips" role="tablist" aria-label="Filter users by status">
          ${tabs.map(([id, name]) => `<button class="chip" role="tab" type="button" aria-selected="${s.status === id}" data-act="user-tab" data-id="${id}">${esc(name)}<span class="cn">${page.counts[id] ?? 0}</span></button>`).join('')}
        </div>
        <form data-form="user-search"><label><span class="sr">Search users</span><input class="field" name="q" type="search" placeholder="Username, name, id or role" value="${esc(s.q)}"></label></form>
      </div>
      <section class="card" aria-label="${esc(label)}">
        ${page.items.length ? `<div class="table-wrap"><table>
          <thead><tr><th class="th th-open"><span class="sr">Details</span></th><th class="th">User</th><th class="th">Sign-in strength</th><th class="th">Roles</th><th class="th">Last login</th><th class="th">Status</th></tr></thead>
          <tbody>${page.items.map((u) => { const [how, n] = signIn(u); return `
            <tr class="row ${state.selected === 'user:' + u.id ? 'selected' : ''}" data-act="open-user" data-id="${esc(u.id)}">
              <td class="td"><button class="open-btn" type="button" data-act="open-user" data-id="${esc(u.id)}" aria-label="Open ${esc(u.username)} details">${chevron}</button></td>
              <td class="td"><div class="who"><div class="avatar">${esc(initials(u.display_name || u.username))}</div>
                <div class="who-name"><b>${esc(u.display_name || u.username)}</b><span class="muted mono">${esc(u.username)}</span></div></div></td>
              <td class="td"><div class="signed"><div class="pips ${n === 1 ? 'weak' : ''}" aria-hidden="true"><span class="pip ${n >= 1 ? 'on' : ''}"></span><span class="pip ${n >= 2 ? 'on' : ''}"></span></div><span class="muted">${esc(how)}</span></div></td>
              <td class="td"><div class="badges">${u.roles.map((r) => `<span class="badge b-calm">${esc(r)}</span>`).join('') || '<span class="muted">None</span>'}</div></td>
              <td class="td muted nowrap">${esc(u.is_service ? '—' : when(u.last_login_at))}</td>
              <td class="td">${badge(u.status)}</td>
            </tr>`; }).join('')}</tbody></table></div>` : '<div class="empty">No users match.</div>'}
        ${pager(s, page, `Showing ${plural(page.items.length, 'user')} of ${page.counts[s.status] ?? 0}`)}
      </section>`;
  }

  async function openUser(id) {
    state.selected = 'user:' + id;
    const r = await api('GET', `/users/${enc(id)}`);
    const u = r.user, [how] = signIn(u);
    people.set(u.id, u.display_name || u.username);
    const live = r.sessions.filter((x) => x.state === 'live');
    const notes = {
      sessions_ended: `A session was ended in the last 7 days after a refresh token was reused, or by a revocation.${u.has_otp ? '' : ' They sign in with a password only.'}`,
      disabled: `Disabled ${full(u.disabled_at)}. They can't sign in until enabled.`,
      service: 'Service rows hold no password. Writes the service makes on its own are recorded under this row.',
    };
    openDrawer({
      kind: 'User', title: u.display_name || u.username, sub: `${u.username}, ${u.id}`, status: u.status, note: notes[u.status] || '',
      facts: [['Sign-in', how], ['Last login', u.is_service ? '—' : when(u.last_login_at)], ['Live sessions', String(live.length)], ['Created', full(u.created_at)]],
      sections: [
        u.is_service ? '' : `<div class="section"><h3>Roles</h3>
          <form class="inline-form" data-form="roles" data-id="${esc(u.id)}">
            <div class="badges">${u.roles.map((role) => `<span class="badge b-calm role-chip">${esc(role)}<button type="button" data-act="drop-role" data-role="${esc(role)}" aria-label="Remove ${esc(role)}">×</button></span>`).join('') || '<span class="muted">No roles</span>'}</div>
            <input type="hidden" name="roles" value="${esc(u.roles.join(','))}">
            <div class="row-fields"><label class="sr" for="add-role">Add a role</label><input class="field" id="add-role" name="add" placeholder="Add a role, e.g. inventory:editor"><button class="btn btn-sm btn-soft" type="submit">Save roles</button></div>
          </form></div>`,
        r.sessions.length ? `<div class="section"><h3>Sessions</h3>${r.sessions.map((x) => `
          <div class="item"><div class="item-row">
            <div class="item-text"><b class="mono">${esc(x.service)}</b><span>${x.started_at ? 'Started ' + esc(when(x.started_at)) : 'Started before sign-in times were kept'}, ${x.state === 'live' ? 'expires ' + esc(full(x.expires_at)) : esc(x.state)}${x.amr && x.amr.includes('otp') ? ', with a code' : ''}</span></div>
            ${x.state === 'live' ? `<button class="btn btn-sm" type="button" data-act="end-session" data-id="${esc(u.id)}" data-session="${esc(x.session)}">End</button>` : badge(x.state)}
          </div></div>`).join('')}</div>` : '',
        r.recent_activity.length ? `<div class="section"><h3>Recent activity</h3>${r.recent_activity.slice(0, 8).map((e) => `
          <div class="item"><div class="item-text"><b>${esc(describe(e))}</b><span>${esc(when(e.at))}${e.actor_user && e.actor_user !== u.id ? ' by ' + esc(nameOf(e.actor_user)) : ''}</span></div></div>`).join('')}</div>` : '',
      ],
      actions: u.is_service ? [] : [
        ['End all sessions', '', 'end-sessions', u.id],
        u.status === 'disabled' ? ['Enable', 'btn-primary', 'user-enable', u.id] : ['Disable', 'btn-danger', 'user-disable', u.id],
      ],
    });
  }

  // ── Audit log ───────────────────────────────────────────────────────────
  function auditQuery(f) {
    return Object.entries(f).filter(([, v]) => v).map(([k, v]) => `${k}=${enc(v)}`).join('&');
  }
  async function renderAudit() {
    const s = state.audit, q = auditQuery(s.f);
    const page = await api('GET', `/audit?limit=50&${q}&cursor=${enc(cursorOf(s))}`);
    s.items = page.items;
    view.innerHTML = `
      <div class="head">
        <div class="head-text"><h1>Audit log</h1>
          <p class="sub">Append-only. Every admin action with who did it, through which service, and the values before and after.</p></div>
        <a class="btn" href="/api/audit?format=csv&${esc(q)}" download="audit.csv">Export CSV</a>
      </div>
      <section class="card" aria-label="Audit entries">
        <form class="filters" data-form="audit-filter">
          <label class="flabel">Actor<input class="field mono" name="actor" value="${esc(s.f.actor)}" placeholder="user id"></label>
          <label class="flabel">Action<input class="field mono" name="action" value="${esc(s.f.action)}" placeholder="service_key.*"></label>
          <label class="flabel">Target<input class="field mono" name="target" value="${esc(s.f.target)}" placeholder="usr_…, svc:…"></label>
          <label class="flabel">From<input class="field" name="from" type="date" value="${esc(s.f.from)}"></label>
          <label class="flabel">To<input class="field" name="to" type="date" value="${esc(s.f.to)}"></label>
          <input type="hidden" name="request_id" value="${esc(s.f.request_id)}">
          <button class="btn btn-soft" type="submit">Apply filters</button>
          ${q ? '<button class="btn" type="button" data-act="clear-filters">Clear</button>' : ''}
        </form>
        ${auditTable(page.items, 'audit', false)}
        ${pager(s, page, `Page ${s.stack.length}, ${plural(page.items.length, 'entry', 'entries')}`)}
      </section>`;
  }

  function openEvent(source, id) {
    const list = source === 'recent' ? state.recent : state.audit.items;
    const e = list.find((x) => String(x.id) === String(id));
    if (!e) return;
    state.selected = 'event:' + id;
    const [kind, cls] = kindOf(e.action);
    const pretty = (v) => v == null ? '—' : JSON.stringify(v, null, 2);
    openDrawer({
      kind: 'Audit entry', title: describe(e), sub: `${e.action}, ${e.request_id || 'no request id'}`, statusHTML: `<span class="badge ${cls}">${esc(kind)}</span>`,
      facts: [['When', full(e.at)], ['Actor', nameOf(e.actor_user)], ['Via', e.via_service ? 'svc:' + e.via_service : '—'],
        ['Second factor', e.amr && e.amr.includes('otp') ? 'One-time code' : e.actor_user ? 'Password only' : 'Automatic'],
        ['Target', e.target], ['Service', e.service], ['Request', e.request_id || '—'], ['IP', e.ip || '—']],
      sections: [`<div class="section"><h3>What changed</h3><div class="diff">
        <div><span class="muted">Before</span><pre class="mono">${esc(pretty(e.before))}</pre></div>
        <div><span class="muted">After</span><pre class="mono">${esc(pretty(e.after))}</pre></div></div></div>`],
      actions: [['Copy request id', '', 'copy', e.request_id || ''], ['Show all for this target', 'btn-soft', 'filter-target', e.target]],
    });
  }

  // ── Schema changes ──────────────────────────────────────────────────────
  async function openSchema(table) {
    const r = await api('GET', '/schema/pending');
    const p = r.items.find((x) => x.table === table);
    if (!p) { toast('Already decided', 'That schema change is no longer waiting.'); return; }
    state.selected = 'schema:' + table;
    openDrawer({
      kind: 'Schema change', title: p.table, sub: `${p.model}, version ${p.version}`, statusHTML: '<span class="badge b-warn">Waiting</span>',
      note: 'This change would lose data, so it waits for a decision. Approving applies it now; denying puts the model file back to its last good version.',
      facts: [['Service', p.service], ['Model', p.model], ['Version', p.version], ['Approved', p.approved ? 'Yes, queued' : 'No']],
      sections: [`<div class="section"><h3>Changes</h3><pre class="mono note">${esc(p.changes)}</pre></div>`],
      actions: [['Approve and apply', 'btn-primary', 'schema-approve', p.table], ['Deny', 'btn-danger', 'schema-deny', p.table]],
    });
  }

  // ── Drawer ──────────────────────────────────────────────────────────────
  let drawerReturn = null;
  function openDrawer(d) {
    drawerReturn = drawerReturn || document.activeElement;
    drawerEl.innerHTML = `<div class="drawer">
      <button class="scrim" type="button" data-act="close" aria-label="Close details"></button>
      <aside class="panel" role="dialog" aria-modal="true" aria-label="${esc(d.title)} details">
        <div class="panel-head"><div class="head-text"><span class="panel-kind">${esc(d.kind)}</span><h2>${esc(d.title)}</h2><span class="muted mono">${esc(d.sub)}</span></div>
          <button class="icon-btn" type="button" data-act="close" aria-label="Close details">${closeIcon}</button></div>
        <div>${d.statusHTML || badge(d.status)}</div>
        ${d.note ? `<div class="note">${esc(d.note)}</div>` : ''}
        <div class="facts">${d.facts.map(([k, v]) => `<div class="fact"><span>${esc(k)}</span><b>${esc(v)}</b></div>`).join('')}</div>
        ${d.sections.join('')}
        <div class="actions">${d.actions.map(([label, cls, act, id]) => `<button class="btn ${cls}" type="button" data-act="${act}" data-id="${esc(id)}">${esc(label)}</button>`).join('')}</div>
      </aside></div>`;
    paintBars(drawerEl);
    drawerEl.querySelector('.panel .icon-btn').focus();
    view.querySelectorAll('.row').forEach((row) => row.classList.toggle('selected', state.selected === `${row.dataset.act.replace('open-', '')}:${row.dataset.id}`));
  }
  function closeDrawer() {
    drawerEl.innerHTML = '';
    state.selected = null;
    view.querySelectorAll('.row.selected').forEach((row) => row.classList.remove('selected'));
    if (drawerReturn && document.contains(drawerReturn)) drawerReturn.focus();
    drawerReturn = null;
  }

  // ── Dialog forms ────────────────────────────────────────────────────────
  // ask shows a form and resolves to its values, or null if cancelled
  function ask({ title, text, fields = [], submit = 'Save', danger = false }) {
    dialog.innerHTML = `<form method="dialog">
      <h2>${esc(title)}</h2>${text ? `<p>${esc(text)}</p>` : ''}
      ${fields.map((f) => `<label class="flabel">${esc(f.label)}
        ${f.type === 'textarea' ? `<textarea class="field mono" name="${f.name}" ${f.required ? 'required' : ''} placeholder="${esc(f.placeholder || '')}"></textarea>`
          : `<input class="field ${f.mono ? 'mono' : ''}" name="${f.name}" type="${f.type || 'text'}" ${f.required ? 'required' : ''} ${f.pattern ? `pattern="${f.pattern}"` : ''} ${f.minlength ? `minlength="${f.minlength}"` : ''} placeholder="${esc(f.placeholder || '')}" value="${esc(f.value || '')}" autocomplete="off">`}
        ${f.hint ? `<span class="login-hint">${esc(f.hint)}</span>` : ''}</label>`).join('')}
      <div class="dialog-actions"><button class="btn" value="cancel" formnovalidate>Cancel</button><button class="btn ${danger ? 'btn-danger' : 'btn-primary'}" value="ok">${esc(submit)}</button></div>
    </form>`;
    return new Promise((resolve) => {
      dialog.onclose = () => {
        if (dialog.returnValue !== 'ok') return resolve(null);
        resolve(Object.fromEntries(new FormData(dialog.querySelector('form'))));
      };
      dialog.returnValue = '';
      dialog.showModal();
      const first = dialog.querySelector('input, textarea');
      if (first) first.focus();
    });
  }

  // change runs a change, says so, and redraws what is open
  async function change(method, path, body, done, reopen) {
    try {
      const r = await api(method, path, body);
      toast(done);
      await render();
      if (reopen) await reopen(r);
      return r;
    } catch (err) { report(err); return null; }
  }

  // ── Actions ─────────────────────────────────────────────────────────────
  const actions = {
    'tab': (d) => { closeDrawer(); setTab(d.tab); },
    'close': () => closeDrawer(),
    'open-service': (d) => openService(d.id).catch(report),
    'open-user': (d) => openUser(d.id).catch(report),
    'open-event': (d) => openEvent(d.source, d.id),
    'open-schema': (d) => openSchema(d.id).catch(report),
    'page-next': (d) => { const s = state[state.tab]; s.stack.push(d.cursor); render(); },
    'page-prev': () => { const s = state[state.tab]; if (s.stack.length > 1) s.stack.pop(); render(); },
    'user-tab': (d) => { state.users.status = d.id; state.users.stack = ['']; render(); },
    'clear-filters': () => { state.audit.f = { actor: '', action: '', target: '', from: '', to: '', request_id: '' }; state.audit.stack = ['']; render(); },
    'copy': async (d) => { try { await navigator.clipboard.writeText(d.id); toast('Request id copied'); } catch (_) { toast('Copy failed', d.id, true); } },
    'filter-target': (d) => { closeDrawer(); state.audit.f = { actor: '', action: '', target: d.id, from: '', to: '', request_id: '' }; state.audit.stack = ['']; setTab('audit'); },

    'new-service': async () => {
      const v = await ask({ title: 'Register a service', text: 'The service team makes the key pair and sends you only the public half.', submit: 'Register',
        fields: [
          { name: 'id', label: 'Service id', required: true, mono: true, pattern: '[a-z][a-z0-9]{1,31}', placeholder: 'inventory', hint: '2 to 32 lower case letters or digits.' },
          { name: 'role', label: 'Role', required: true, mono: true, placeholder: 'reader' },
          { name: 'kid', label: 'Key id (optional)', mono: true, placeholder: 'inventory-1' },
          { name: 'public_key', label: 'Public key (optional)', type: 'textarea', placeholder: 'Base64 Ed25519 public key' },
        ] });
      if (!v) return;
      const body = { id: v.id.trim(), role: v.role.trim() };
      if (v.kid.trim() || v.public_key.trim()) body.key = { kid: v.kid.trim(), public_key: v.public_key.trim() };
      change('POST', '/services', body, `Registered ${body.id}`, () => openService(body.id));
    },
    'add-key': async (d) => {
      const v = await ask({ title: `Add a key to ${d.id}`, submit: 'Add key', fields: [
        { name: 'kid', label: 'Key id', required: true, mono: true, placeholder: `${d.id}-2` },
        { name: 'public_key', label: 'Public key', type: 'textarea', required: true, placeholder: 'Base64 Ed25519 public key' }] });
      if (v) change('POST', `/services/${enc(d.id)}/keys`, { kid: v.kid.trim(), public_key: v.public_key.trim() }, `Added ${v.kid}`, () => openService(d.id));
    },
    'revoke-key': async (d) => {
      const v = await ask({ title: `Revoke ${d.kid}?`, text: 'The key can\'t sign again, and every token the service holds now is refused. It signs in again with another key.', submit: 'Revoke key', danger: true,
        fields: [{ name: 'reason', label: 'Reason (optional)', placeholder: 'Rotated, leaked, …' }] });
      if (v) change('POST', `/services/${enc(d.id)}/keys/${enc(d.kid)}/revoke`, { reason: v.reason.trim() }, `Revoked ${d.kid}`, () => openService(d.id));
    },
    'service-role': async (d) => {
      const v = await ask({ title: `Change ${d.id}'s role`, text: 'Its current tokens are refused, so the new role applies at once.', submit: 'Change role',
        fields: [{ name: 'role', label: 'Role', required: true, mono: true }] });
      if (v) change('PATCH', `/services/${enc(d.id)}`, { role: v.role.trim() }, `Changed ${d.id}'s role`, () => openService(d.id));
    },
    'service-disable': async (d) => {
      const v = await ask({ title: `Disable ${d.id}?`, text: 'Its tokens are refused, and it can\'t get new ones. People signed in through it lose access too.', submit: 'Disable', danger: true });
      if (v) change('POST', `/services/${enc(d.id)}/disable`, undefined, `Disabled ${d.id}`, () => openService(d.id));
    },
    'service-enable': (d) => change('POST', `/services/${enc(d.id)}/enable`, undefined, `Enabled ${d.id}`, () => openService(d.id)),

    'new-user': async () => {
      const v = await ask({ title: 'Create a user', submit: 'Create', fields: [
        { name: 'username', label: 'Username', required: true, mono: true },
        { name: 'display_name', label: 'Name' },
        { name: 'password', label: 'Password (optional)', type: 'password', minlength: 12, hint: 'At least 12 characters. Leave empty for someone who signs in another way.' },
        { name: 'roles', label: 'Roles', mono: true, placeholder: 'user, inventory:editor' }] });
      if (!v) return;
      const roles = v.roles.split(',').map((r) => r.trim()).filter(Boolean);
      change('POST', '/users', { username: v.username.trim(), display_name: v.display_name.trim(), password: v.password, roles }, `Created ${v.username}`, (r) => openUser(r.user.id));
    },
    'drop-role': (d, el) => {
      const form = el.closest('form'), input = form.querySelector('input[name="roles"]');
      input.value = input.value.split(',').filter((r) => r && r !== d.role).join(',');
      el.closest('.role-chip').remove();
    },
    'end-session': (d) => change('POST', `/users/${enc(d.id)}/sessions/${enc(d.session)}/revoke`, undefined, 'Session ended', () => openUser(d.id)),
    'end-sessions': async (d) => {
      const v = await ask({ title: 'End every session?', text: 'They\'re signed out everywhere and have to sign in again.', submit: 'End sessions', danger: true });
      if (v) change('POST', `/users/${enc(d.id)}/sessions/revoke`, undefined, 'Every session ended', () => openUser(d.id));
    },
    'user-disable': async (d) => {
      const v = await ask({ title: 'Disable this user?', text: 'Every session ends and they can\'t sign in until enabled.', submit: 'Disable', danger: true });
      if (v) change('POST', `/users/${enc(d.id)}/disable`, undefined, 'User disabled', () => openUser(d.id));
    },
    'user-enable': (d) => change('POST', `/users/${enc(d.id)}/enable`, undefined, 'User enabled', () => openUser(d.id)),

    'schema-approve': async (d) => {
      try {
        const r = await api('POST', `/schema/${enc(d.id)}/approve`);
        if (r.applied) toast('Schema change applied', d.id); else toast('Approved, not applied yet', r.message, true);
        closeDrawer(); render();
      } catch (err) { report(err); }
    },
    'schema-deny': async (d) => {
      const v = await ask({ title: `Deny the change to ${d.id}?`, text: 'The model file goes back to its last good version, on every instance.', submit: 'Deny', danger: true,
        fields: [{ name: 'reason', label: 'Reason (optional)' }] });
      if (!v) return;
      try { await api('POST', `/schema/${enc(d.id)}/deny`, { reason: v.reason.trim() }); toast('Schema change denied', d.id); closeDrawer(); render(); }
      catch (err) { report(err); }
    },
  };

  document.addEventListener('click', (e) => {
    const t = e.target.closest('[data-act]');
    if (!t || !actions[t.dataset.act]) return;
    // A button inside a row handles the click once, not again for the row
    if (t.tagName === 'TR' && e.target.closest('button')) return;
    e.preventDefault();
    actions[t.dataset.act](t.dataset, t);
  });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && drawerEl.innerHTML && !dialog.open) closeDrawer(); });

  // Forms inside the page
  document.addEventListener('submit', (e) => {
    const form = e.target.closest('[data-form]');
    if (!form) return;
    e.preventDefault();
    const data = Object.fromEntries(new FormData(form));
    switch (form.dataset.form) {
      case 'service-search': state.services.q = data.q.trim(); state.services.stack = ['']; render(); break;
      case 'user-search': state.users.q = data.q.trim(); state.users.stack = ['']; render(); break;
      case 'audit-filter': state.audit.f = data; state.audit.stack = ['']; render(); break;
      case 'roles': {
        const roles = data.roles.split(',').concat(data.add.split(',')).map((r) => r.trim()).filter(Boolean);
        change('PUT', `/users/${enc(form.dataset.id)}/roles`, { roles: [...new Set(roles)] }, 'Roles saved', () => openUser(form.dataset.id));
        break;
      }
    }
  });

  // The top search: a request id goes to the audit log, anything else to users
  $('search').addEventListener('submit', (e) => {
    e.preventDefault();
    const q = $('search-q').value.trim();
    if (!q) return;
    closeDrawer();
    if (/^[A-Za-z0-9._-]{16,64}$/.test(q) && !q.includes('.')) {
      state.audit.f = { actor: '', action: '', target: '', from: '', to: '', request_id: q }; state.audit.stack = [''];
      setTab('audit');
    } else {
      state.users.q = q; state.users.status = 'all'; state.users.stack = [''];
      setTab('users');
    }
  });

  document.querySelectorAll('.nav').forEach((b) => b.addEventListener('click', () => { closeDrawer(); setTab(b.dataset.tab); }));
  window.addEventListener('hashchange', () => { const t = location.hash.slice(1); if (t && t !== state.tab) setTab(t); });

  $('today').textContent = new Date().toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' });
  $('me-initials').textContent = initials($('me-name').textContent);
  const start = location.hash.slice(1);
  loadPeople().finally(() => setTab(['overview', 'services', 'users', 'audit'].includes(start) ? start : 'overview'));
})();
