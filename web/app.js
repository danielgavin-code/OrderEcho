// OrderEcho agent GUI (A5). Vanilla JS, no dependencies, no network calls
// beyond this agent service. Every action is a call to /api/v1 — the same
// endpoints MCP clients use — so the GUI has no logic of its own: the agent
// decides what happened and whether it passed.
(function () {
  'use strict';

  var CSRF = meta('orderecho-csrf');
  var PAGE = meta('orderecho-page') || 'dashboard';
  var app = document.getElementById('app');
  var listeners = {}; // event type -> [fn]
  var state = { sessions: [], about: null };

  function meta(name) {
    var m = document.querySelector('meta[name="' + name + '"]');
    return m ? m.getAttribute('content') : '';
  }

  // ------------------------------------------------------------ DOM helper

  // el('tag.class', {attr}, child, ...) — children are nodes or text.
  function el(spec, attrs) {
    var parts = spec.split('.');
    var node = document.createElement(parts[0] || 'div');
    if (parts.length > 1) node.className = parts.slice(1).join(' ');
    var kids = Array.prototype.slice.call(arguments, 2);
    if (attrs && (attrs.nodeType || typeof attrs !== 'object' || Array.isArray(attrs))) { kids.unshift(attrs); attrs = null; }
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v === undefined || v === null || v === false) return;
        if (k.slice(0, 2) === 'on') node.addEventListener(k.slice(2), v);
        else if (k === 'value') node.value = v;
        else node.setAttribute(k, v === true ? '' : v);
      });
    }
    add(node, kids);
    return node;
  }
  function add(node, kids) {
    kids.forEach(function (k) {
      if (k === undefined || k === null || k === false) return;
      if (Array.isArray(k)) { add(node, k); return; }
      node.appendChild(k.nodeType ? k : document.createTextNode(String(k)));
    });
  }
  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); return node; }

  function badgeClass(s) {
    if (s === 'N/A') return 'NA';
    if (s === 'NOT CERTIFIED') return 'NOT-CERTIFIED';
    return String(s || '').replace(/ /g, '-');
  }
  function badge(s, cls) { return el('span.badge ' + (cls || badgeClass(s)), s || '—'); }
  function table(headers, rows) {
    return el('div.table-wrap', el('table',
      el('thead', el('tr', headers.map(function (h) { return el('th', h); }))),
      el('tbody', rows)));
  }
  function td(x, cls) { return el('td' + (cls ? '.' + cls : ''), x === undefined || x === '' ? '—' : x); }
  function card(title) { return el('div.card', title ? el('h2', title) : null, Array.prototype.slice.call(arguments, 1)); }
  function field(label, input) { return el('div.field', el('label', label), input); }
  function select(name, options, value) {
    return el('select', { name: name }, options.map(function (o) {
      var v = Array.isArray(o) ? o[0] : o, t = Array.isArray(o) ? o[1] : o;
      return el('option', { value: v, selected: v === value }, t);
    }));
  }
  function ago(iso) {
    if (!iso) return '—';
    var s = Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 1000));
    return s < 120 ? s + 's ago' : Math.round(s / 60) + 'm ago';
  }
  function countsLine(c) {
    if (!c) return '';
    return ['PASS', 'FAIL', 'BLOCKED', 'PENDING', 'N/A', 'ERROR', 'NOT_RUN'].filter(function (k) { return c[k]; })
      .map(function (k) { return k + ' ' + c[k]; }).join(', ');
  }

  // ------------------------------------------------------------ API

  function call(tool, args) {
    return fetch('/api/v1/' + tool, {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-OrderEcho-CSRF': CSRF, 'X-OrderEcho-Client': 'gui' },
      body: JSON.stringify(args || {})
    }).then(function (r) {
      return r.json().then(function (body) { if (!r.ok) throw body; return body; });
    });
  }
  function get(path) {
    return fetch(path, { credentials: 'same-origin' }).then(function (r) {
      return r.json().then(function (body) { if (!r.ok) throw body; return body; });
    });
  }
  function errText(e) {
    if (e && e.error) return e.error + ': ' + e.detail + (e.hint ? '\nHint: ' + e.hint : '');
    return String(e);
  }

  var toastTimer;
  function toast(text, isError) {
    var t = document.getElementById('toast');
    clear(t);
    t.className = 'toast' + (isError ? ' error' : '');
    add(t, [el('div.small', text)]);
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.className = 'toast hidden'; }, isError ? 12000 : 6000);
  }
  // run an action: show its summary (or error) as a toast.
  function act(promise, after) {
    return promise.then(function (r) { toast(r.summary); if (after) after(r); return r; })
      .catch(function (e) { toast(errText(e), true); throw e; });
  }

  function modal(title, body, onClose) {
    var back = document.getElementById('modal');
    clear(back);
    var close = function () { back.className = 'modal-back hidden'; clear(back); if (onClose) onClose(); };
    var box = el('div.modal', el('header', el('h2', title), el('button', { type: 'button', onclick: close }, 'Close')), body);
    back.appendChild(box);
    back.className = 'modal-back';
    back.onclick = function (ev) { if (ev.target === back) close(); };
    return close;
  }

  // ------------------------------------------------------------ live updates

  function on(type, fn) { (listeners[type] = listeners[type] || []).push(fn); }
  function startEvents() {
    var conn = document.getElementById('conn');
    if (!window.EventSource) { conn.textContent = 'live updates: not supported by this browser'; return; }
    var es = new EventSource('/api/v1/events');
    es.onopen = function () { conn.textContent = 'live updates: connected'; };
    es.onerror = function () { conn.textContent = 'live updates: reconnecting…'; };
    ['session', 'message', 'order', 'cert', 'dropped', 'reset'].forEach(function (type) {
      es.addEventListener(type, function (ev) {
        var data = {};
        try { data = JSON.parse(ev.data); } catch (e) { /* keep {} */ }
        if (type === 'dropped' || type === 'reset') {
          toast('Live updates: some events were missed (' + (data.dropped || data.reason) + '); the page reloaded its data.');
          (listeners.refresh || []).forEach(function (fn) { fn(); });
        }
        (listeners[type] || []).forEach(function (fn) { fn(data); });
      });
    });
  }
  // debounce: run fn at most once per ms while events stream in.
  function debounce(fn, ms) {
    var t;
    return function () { clearTimeout(t); t = setTimeout(fn, ms); };
  }

  function loadSessions() {
    return call('list_sessions').then(function (r) { state.sessions = r.result.sessions || []; return state.sessions; });
  }
  function sessionSelect(name, value, withAll) {
    var opts = state.sessions.map(function (s) { return [s.session_id, s.session_id + ' (' + s.fix_version + ', ' + s.state + ')']; });
    if (withAll) opts.unshift(['', 'all sessions']);
    return select(name, opts, value);
  }
  function sessionByID(id) {
    for (var i = 0; i < state.sessions.length; i++) if (state.sessions[i].session_id === id) return state.sessions[i];
    return null;
  }

  // ------------------------------------------------------------ Dashboard

  function dashboard() {
    var sessBox = el('div'), runsBox = el('div'), svcBox = el('div');
    add(clear(app), [el('h1', 'Dashboard'), card('Sessions', sessBox), card('Recent certification runs', runsBox), card('Service', svcBox)]);
    var status = {};
    function renderSessions() {
      var rows = state.sessions.map(function (s) {
        var st = status[s.session_id] || {};
        var connected = s.connected;
        var btn = connected
          ? el('button.danger', { type: 'button', onclick: function () { act(call('disconnect_session', { session_id: s.session_id }), refresh); } }, 'Disconnect')
          : el('button.primary', { type: 'button', disabled: !!s.cert_run, onclick: function () { act(call('connect_session', { session_id: s.session_id }), refresh); } }, 'Connect');
        var last = st.last_in_at || '';
        return el('tr',
          td(el('strong', s.session_id)), td(s.fix_version), td(s.route), td(el('span.mono.small', s.address)),
          td([badge(s.state), s.external ? [' ', badge('EXTERNAL', 'WARN')] : null, s.cert_run ? [' ', el('span.small', 'cert run ' + s.cert_run)] : null]),
          td(st.next_out !== undefined ? st.next_out + ' / ' + st.next_in : '—', 'num mono'),
          td(el('span', { 'data-ago': last }, last ? ago(last) : '—')),
          td(s.open_orders, 'num'), td(btn));
      });
      clear(sessBox).appendChild(table(['Session', 'Version', 'Route', 'Address', 'State', 'Seq out / in', 'Last inbound (heartbeat age)', 'Open orders', ''], rows));
    }
    function refresh() {
      return loadSessions().then(function (list) {
        return Promise.all(list.map(function (s) {
          return call('session_status', { session_id: s.session_id }).then(function (r) { status[s.session_id] = r.result; }).catch(function () {});
        }));
      }).then(renderSessions).catch(function (e) { toast(errText(e), true); });
    }
    function loadRuns() {
      return get('/api/v1/certs?limit=10').then(function (r) {
        var rows = r.runs.map(function (run) {
          return el('tr', td(el('span.mono', run.run_id)), td(run.suite), td(run.target), td(run.session_id), td(badge(run.state)),
            td(run.verdict ? badge(run.verdict) : '—'), td(el('span.small', countsLine(run.counts))),
            td(run.report_url ? el('a', { href: run.report_url, target: '_blank', rel: 'noopener' }, 'report') : '—'));
        });
        clear(runsBox).appendChild(rows.length ? table(['Run', 'Suite', 'Target', 'Session', 'State', 'Verdict', 'Counts', ''], rows) : el('p.muted', 'No certification runs yet.'));
      });
    }
    get('/api/v1/health').then(function (h) {
      add(clear(svcBox), [el('dl.kv', el('dt', 'Version'), el('dd', h.version + ' (' + h.build + ')'), el('dt', 'PID'), el('dd', h.pid),
        el('dt', 'Since'), el('dd', h.started), el('dt', 'Config'), el('dd', el('span.mono.small', h.config)),
        el('dt', 'MCP over HTTP'), el('dd', h.mcp_http ? 'on (/mcp, bearer token)' : 'off'))]);
    });
    refresh(); loadRuns();
    var later = debounce(refresh, 400);
    on('session', later);
    on('message', debounce(refresh, 1500));
    on('cert', debounce(loadRuns, 500));
    on('refresh', function () { refresh(); loadRuns(); });
    setInterval(function () {
      document.querySelectorAll('[data-ago]').forEach(function (n) { if (n.getAttribute('data-ago')) n.textContent = ago(n.getAttribute('data-ago')); });
    }, 1000);
  }

  // ------------------------------------------------------------ Orders

  function timelineModal(sessionID, clOrdID) {
    call('order_timeline', { session_id: sessionID, cl_ord_id: clOrdID }).then(function (r) {
      var t = r.result;
      var steps = (t.steps || []).map(function (s) {
        return el('tr', td(el('span.mono.tiny', s.time)), td(badge(s.direction, s.direction)), td(s.msg_type_name),
          td((s.exec_type_name || '') + (s.ord_status_name ? ' / ' + s.ord_status_name : '')), td(s.order_qty, 'num'),
          td(s.last_qty ? s.last_qty + ' @ ' + s.last_px : '', 'num'), td(s.cum_qty, 'num'), td(s.leaves_qty, 'num'), td(s.avg_px, 'num'), td(s.text));
      });
      var checks = (t.checks || []).map(function (c) {
        return el('tr', td(el('span.mono', c.name)), td(badge(c.status)), td(c.explanation));
      });
      modal('Order ' + t.seed, [el('p.small', r.summary),
        el('h3', 'Messages'), table(['Time (UTC)', 'Dir', 'Type', 'Exec / status', 'Qty', 'Last', 'Cum', 'Leaves', 'Avg', 'Text'], steps),
        el('h3', 'The 11 checks — verdict ', badge(t.verdict)), table(['Check', 'Status', 'Explanation'], checks)]);
    }).catch(function (e) { toast(errText(e), true); });
  }

  function replaceModal(sessionID, o, refresh) {
    var qty = el('input', { type: 'number', min: 1, step: 1, value: o.order_qty });
    var px = el('input', { type: 'text', value: o.price || '', disabled: o.ord_type !== 'LMT' });
    var close = modal('Replace ' + o.cl_ord_id, [
      el('p.small', 'Sends an OrderCancelReplaceRequest (35=G). Quantity is the new total, not the change.'),
      field('New total quantity', qty), field('New limit price', px),
      el('div.row', el('button.primary', { type: 'button', onclick: function () {
        var args = { session_id: sessionID, cl_ord_id: o.cl_ord_id, qty: qty.value };
        if (o.ord_type === 'LMT' && px.value) args.price = px.value;
        if (sessionByID(sessionID) && sessionByID(sessionID).external && !window.confirm('This session is EXTERNAL (a real counterparty). Send the replace?')) return;
        if (sessionByID(sessionID) && sessionByID(sessionID).external) args.confirm_external = true;
        act(call('replace_order', args), function () { close(); refresh(); });
      } }, 'Send replace'))]);
  }

  function orders() {
    var formBox = el('div'), listBox = el('div'), current = '';
    add(clear(app), [el('h1', 'Orders'), el('div.row', el('label', 'Session'), el('span', { id: 'sess-pick' })),
      card('Send an order', formBox), card('Orders', listBox)]);
    function renderForm() {
      var s = sessionByID(current) || {};
      var f = {
        symbol: el('input', { type: 'text', name: 'symbol', value: 'AAPL', required: true }),
        side: select('side', ['buy', 'sell', 'short'], 'buy'),
        qty: el('input', { type: 'number', name: 'qty', min: 1, step: 1, value: 100 }),
        type: select('ord_type', [['mkt', 'market'], ['lmt', 'limit']], 'mkt'),
        price: el('input', { type: 'text', name: 'price', placeholder: 'limit only', disabled: true }),
        tif: select('tif', [['', '(none)'], 'day', 'gtc', 'opg', 'ioc', 'fok', 'gtx'], ''),
        wait: select('wait_for', [['ack', 'wait for ack'], ['terminal', 'wait until done'], ['none', "don't wait"]], 'ack'),
        ext: el('input', { type: 'checkbox', name: 'confirm_external' })
      };
      f.type.addEventListener('change', function () { f.price.disabled = f.type.value !== 'lmt'; });
      var send = el('button.primary', { type: 'button', disabled: !s.connected, onclick: function () {
        var args = { session_id: current, symbol: f.symbol.value.trim(), side: f.side.value, qty: f.qty.value, ord_type: f.type.value, wait_for: f.wait.value };
        if (f.type.value === 'lmt') args.price = f.price.value.trim();
        if (f.tif.value) args.tif = f.tif.value;
        if (s.external) {
          if (!f.ext.checked) { toast('This session is external: tick the confirmation first.', true); return; }
          args.confirm_external = true;
        }
        send.disabled = true;
        act(call('send_order', args), refresh).finally(function () { send.disabled = !s.connected; });
      } }, 'Send order');
      add(clear(formBox), [
        s.connected ? null : el('p.notice.warn.small', 'Session ' + current + ' is not connected — connect it on the Dashboard first.'),
        field('Symbol', f.symbol), field('Side', f.side), field('Quantity', f.qty), field('Type', f.type), field('Limit price', f.price),
        field('Time in force', f.tif), field('Wait', f.wait),
        s.external ? field('External counterparty', el('label', f.ext, ' I confirm this goes to a real counterparty')) : null,
        el('div', send)]);
    }
    function refresh() {
      if (!current) return;
      call('list_orders', { session_id: current }).then(function (r) {
        var rows = (r.result.orders || []).slice().reverse().map(function (o) {
          var working = ['FILLED', 'CANCELED', 'REJECTED'].indexOf(o.state) < 0;
          return el('tr.clickable', { onclick: function () { timelineModal(current, o.cl_ord_id); } },
            td(el('span.mono.small', o.cl_ord_id)), td(el('span.mono.small', o.order_id)), td(o.symbol), td(o.side),
            td(o.order_qty, 'num'), td(o.ord_type), td(o.price, 'num'), td(badge(o.state)), td(o.cum_qty, 'num'), td(o.leaves_qty, 'num'),
            td(o.avg_px, 'num'), td(badge(o.verdict)),
            td(working && o.on_current_connection ? [
              el('button', { type: 'button', onclick: function (ev) { ev.stopPropagation(); replaceModal(current, o, refresh); } }, 'Replace'), ' ',
              el('button.danger', { type: 'button', onclick: function (ev) {
                ev.stopPropagation();
                var args = { session_id: current, cl_ord_id: o.cl_ord_id };
                var s = sessionByID(current);
                if (s && s.external) { if (!window.confirm('External counterparty: send the cancel?')) return; args.confirm_external = true; }
                act(call('cancel_order', args), refresh);
              } }, 'Cancel')] : null));
        });
        clear(listBox).appendChild(rows.length ? table(['ClOrdID', 'OrderID', 'Symbol', 'Side', 'Qty', 'Type', 'Price', 'State', 'Cum', 'Leaves', 'Avg', 'Checks', ''], rows)
          : el('p.muted', 'No orders on ' + current + ' yet.'));
      }).catch(function (e) { toast(errText(e), true); });
    }
    loadSessions().then(function () {
      var connected = state.sessions.filter(function (s) { return s.connected; });
      current = (connected[0] || state.sessions[0] || {}).session_id || '';
      var pick = sessionSelect('session', current);
      pick.addEventListener('change', function () { current = pick.value; renderForm(); refresh(); });
      clear(document.getElementById('sess-pick')).appendChild(pick);
      renderForm(); refresh();
    });
    var later = debounce(refresh, 250);
    on('order', function (d) { if (d.session_id === current) later(); });
    on('session', debounce(function () { loadSessions().then(renderForm); }, 300));
    on('refresh', refresh);
  }

  // ------------------------------------------------------------ Messages

  function messages() {
    var feed = el('div.feed'), all = [], MAX = 1000;
    var fSess = el('select'), fType = select('type', [['', 'all types'], ['8', 'ExecutionReport'], ['D', 'NewOrderSingle'], ['F', 'OrderCancelRequest'],
      ['G', 'OrderCancelReplaceRequest'], ['9', 'OrderCancelReject'], ['3', 'Reject'], ['j', 'BusinessMessageReject'], ['A', 'Logon'], ['5', 'Logout'],
      ['0', 'Heartbeat'], ['1', 'TestRequest'], ['2', 'ResendRequest'], ['4', 'SequenceReset']], '');
    var fID = el('input', { type: 'text', placeholder: 'ClOrdID contains…' });
    var fRej = el('input', { type: 'checkbox' });
    add(clear(app), [el('h1', 'Messages'),
      el('div.card', el('div.row', field('Session', fSess), field('Type', fType), field('ClOrdID', fID), field('Rejects only', fRej),
        el('span.spacer'), el('span.small.muted', { id: 'msg-count' }, ''))),
      el('div.card', el('div.feed.small.muted', el('div.msg.head', el('span', 'time'), el('span', 'dir'), el('span', 'seq'), el('span', 'type'), el('span', 'key fields'))), feed)]);
    function fieldVal(m, tag) {
      var f = (m.fields || []).filter(function (x) { return x.tag === tag; })[0];
      return f ? f.value : '';
    }
    function isReject(m) {
      return m.msg_type === '3' || m.msg_type === 'j' || m.msg_type === '9' || (m.msg_type === '8' && (fieldVal(m, 39) === '8' || fieldVal(m, 150) === '8')) || m.direction === 'disc';
    }
    function keep(m) {
      if (fSess.value && m.session_id !== fSess.value) return false;
      if (fType.value && m.msg_type !== fType.value) return false;
      if (fID.value && (m.cl_ord_id || fieldVal(m, 11) + ' ' + fieldVal(m, 41)).indexOf(fID.value.trim()) < 0) return false;
      if (fRej.checked && !isReject(m)) return false;
      return true;
    }
    function row(m) {
      var flags = (m.flags || []).map(function (f) { return [badge(f, f), ' ']; });
      return el('div.msg.' + (m.direction || 'in'), { onclick: function () { decodeModal(m); } },
        el('span', (m.ts || '').slice(11, 23)), el('span.dir', m.direction === 'in' ? 'IN' : m.direction === 'out' ? 'OUT' : 'DISC'),
        el('span', m.seq || ''), el('span', m.msg_type_name || m.msg_type), el('span.line', flags, m.line || m.summary));
    }
    function render() {
      var shown = all.filter(keep);
      clear(feed);
      shown.slice(-400).reverse().forEach(function (m) { feed.appendChild(row(m)); });
      document.getElementById('msg-count').textContent = shown.length + ' of ' + all.length + ' message(s), newest first';
    }
    function decodeModal(m) {
      var rows = (m.fields || []).map(function (f) { return el('tr', td(f.tag, 'num mono'), td(f.name), td(el('span.mono', f.value)), td(f.meaning)); });
      modal((m.msg_type_name || 'Message') + ' — ' + m.session_id + ' ' + (m.direction || ''), [
        el('p.small.mono', m.summary || m.line), (m.flags || []).length ? el('p', (m.flags || []).map(function (f) { return [badge(f, f), ' ']; })) : null,
        table(['Tag', 'Name', 'Value', 'Meaning'], rows)]);
    }
    function load() {
      return loadSessions().then(function (list) {
        clear(fSess); add(fSess, [el('option', { value: '' }, 'all sessions')].concat(list.map(function (s) { return el('option', { value: s.session_id }, s.session_id); })));
        return Promise.all(list.map(function (s) {
          return call('recent_messages', { session_id: s.session_id, limit: 200 }).then(function (r) {
            return (r.result.messages || []).map(function (m) {
              m.session_id = s.session_id;
              m.cl_ord_id = fieldVal(m, 11);
              m.line = m.summary;
              m.flags = [];
              if (fieldVal(m, 43) === 'Y') m.flags.push('POSSDUP');
              if (fieldVal(m, 97) === 'Y') m.flags.push('POSSRESEND');
              return m;
            });
          }).catch(function () { return []; });
        }));
      }).then(function (lists) {
        all = [].concat.apply([], lists).sort(function (a, b) { return a.ts < b.ts ? -1 : a.ts > b.ts ? 1 : 0; });
        render();
      });
    }
    [fSess, fType, fRej].forEach(function (n) { n.addEventListener('change', render); });
    fID.addEventListener('input', debounce(render, 200));
    load();
    var later = debounce(render, 150);
    on('message', function (m) { all.push(m); if (all.length > MAX) all = all.slice(-MAX); later(); });
    on('refresh', load);
  }

  // ------------------------------------------------------------ Certifications

  function certifications() {
    var startBox = el('div'), runsBox = el('div'), resBox = el('div'), progBox = el('div'), catBox = el('div'), suites = [], targets = [];
    add(clear(app), [el('h1', 'Certifications'), card('Suites and targets', catBox), card('Start a run', startBox), card('Live progress', progBox),
      card('Runs', runsBox), el('div', { id: 'results' }, resBox)]);
    var progress = {};
    function renderProgress() {
      var ids = Object.keys(progress).filter(function (k) { return progress[k].state === 'running'; });
      clear(progBox);
      if (!ids.length) { progBox.appendChild(el('p.muted', 'No run in progress.')); return; }
      ids.forEach(function (id) {
        var p = progress[id], bar = el('div');
        bar.style.width = (p.total ? Math.round(100 * p.done / p.total) : 0) + '%';
        add(progBox, [el('p', el('strong.mono', id), ' ', p.suite, ' vs ', p.target, ' on ', p.session_id, ' — ', p.done + '/' + p.total,
          p.current_case ? ' — running case ' + p.current_case : '', ' ', el('span.small', countsLine(p.counts))), el('div.progress', bar)]);
      });
    }
    function renderStart() {
      var suite = select('suite', suites.map(function (s) { return [s.suite, s.suite + ' (' + s.fix_version + ', ' + s.cases + ' cases)']; }));
      var target = select('target', targets.map(function (t) { return [t.target, t.target + (t.control_api ? ' (control API)' : '')]; }));
      var sess = sessionSelect('session');
      var section = el('select');
      var cases = el('input', { type: 'text', placeholder: 'e.g. 4.1,4.3 (optional)' });
      var ext = el('input', { type: 'checkbox' });
      function fillSections() {
        var s = suites.filter(function (x) { return x.suite === suite.value; })[0] || { sections: [] };
        clear(section);
        add(section, [el('option', { value: '' }, 'whole suite')].concat((s.sections || []).map(function (sec) {
          return el('option', { value: sec.id }, sec.id + ' — ' + sec.name + ' (' + sec.cases + ')');
        })));
      }
      suite.addEventListener('change', fillSections); fillSections();
      add(clear(startBox), [field('Suite', suite), field('Target', target), field('Session', sess), field('Section', section), field('Cases', cases),
        field('External session', el('label', ext, ' I confirm (real counterparty)')),
        el('p.small.muted', 'The run takes the session over: it logs it out if connected, logs on with a sequence reset, and leaves it disconnected.'),
        el('button.primary', { type: 'button', onclick: function () {
          var args = { suite: suite.value, target: target.value, session_id: sess.value };
          if (section.value) args.section = section.value;
          var ids = cases.value.split(',').map(function (x) { return x.trim(); }).filter(Boolean);
          if (ids.length) args.cases = ids;
          if (ext.checked) args.confirm_external = true;
          act(call('start_cert_run', args), function (r) { progress[r.result.run_id] = { run_id: r.result.run_id, state: 'running', done: 0, total: r.result.total, suite: r.result.suite, target: r.result.target, session_id: r.result.session_id }; renderProgress(); loadRuns(); });
        } }, 'Start run')]);
    }
    function loadRuns() {
      return get('/api/v1/certs?limit=30').then(function (r) {
        var rows = r.runs.map(function (run) {
          if (run.state === 'running') progress[run.run_id] = run;
          return el('tr.clickable', { onclick: function () { showResults(run.run_id); } },
            td(el('span.mono', run.run_id)), td(run.suite), td(run.target), td(run.session_id), td(badge(run.state)),
            td(run.verdict ? badge(run.verdict) : '—'), td(el('span.small', countsLine(run.counts))),
            td(run.report_url ? el('a', { href: run.report_url, target: '_blank', rel: 'noopener', onclick: function (ev) { ev.stopPropagation(); } }, 'report') : '—'));
        });
        clear(runsBox).appendChild(rows.length ? table(['Run', 'Suite', 'Target', 'Session', 'State', 'Verdict', 'Counts', ''], rows) : el('p.muted', 'No runs yet.'));
        renderProgress();
      });
    }
    function attestModal(runID, c) {
      var st = select('status', [['pass', 'pass'], ['fail', 'fail'], ['na', 'not applicable']], 'pass');
      var by = el('input', { type: 'text', placeholder: 'your name', required: true });
      var note = el('textarea', { placeholder: 'what you checked or confirmed' });
      var close = modal('Attest case ' + c.id + ' — ' + c.title, [
        el('p.small', el('strong', 'Task: '), c.task), el('p.small', el('strong', 'Current: '), badge(c.status), ' ', c.reason),
        field('Status', st), field('Your name', by), el('div', el('label', 'Note'), note),
        el('p.notice.small', 'An attestation is a human statement recorded verbatim in results.json, the report and the audit trail, with your name and the time.'),
        el('button.primary', { type: 'button', onclick: function () {
          if (!by.value.trim() || !note.value.trim()) { toast('Name and note are both required.', true); return; }
          var sure = window.confirm('Record that ' + by.value.trim() + ' attests case ' + c.id + ' as ' + st.value.toUpperCase() + '?\n\nNote: ' + note.value.trim() + '\n\nThis is recorded as a human attestation.');
          if (!sure) return;
          act(call('attest_cert_case', { run_id: runID, case_id: c.id, status: st.value, by: by.value.trim(), note: note.value.trim(), user_confirmed: true }),
            function () { close(); showResults(runID); loadRuns(); });
        } }, 'Record attestation…')]);
    }
    function showResults(runID) {
      call('cert_run_results', { run_id: runID, only: 'all' }).then(function (r) {
        var x = r.result;
        var verifyOut = el('span.small');
        var rows = (x.cases || []).map(function (c) {
          return el('tr', td(el('span.mono', c.id)), td(c.title), td(c.required ? 'req' : 'opt'), td(c.mode), td(badge(c.status)), td(el('span.small', c.reason)),
            td(c.attestable ? el('button', { type: 'button', onclick: function () { attestModal(runID, c); } }, c.attestation ? 'Re-attest' : 'Attest') : null));
        });
        add(clear(resBox), [card('Run ' + runID,
          el('div.row', badge(x.state), el('span', x.suite + ' vs ' + x.target + ' on ' + x.session_id), el('span.small', 'exit ' + x.exit_code + ' — ' + x.exit_meaning), el('span.spacer'),
            el('a.button', { href: '/certs/' + runID + '/report', target: '_blank', rel: 'noopener' }, 'Open report'),
            el('button', { type: 'button', onclick: function () {
              get('/api/v1/certs/' + encodeURIComponent(runID) + '/verify').then(function (v) {
                verifyOut.textContent = !v.sealed ? 'no integrity record (run predates 0.5.0)' : v.ok ? 'intact: ' + v.files.length + ' file(s) match' : 'TAMPERED: ' + v.problems + ' file(s) differ';
                verifyOut.className = 'badge ' + (!v.sealed ? 'NA' : v.ok ? 'PASS' : 'FAIL');
              }).catch(function (e) { toast(errText(e), true); });
            } }, 'Verify integrity'), verifyOut),
          el('p.small', 'All: ' + countsLine(x.counts) + ' · Required: ' + countsLine(x.required_counts)),
          table(['Case', 'Title', 'Req', 'Mode', 'Status', 'Reason', ''], rows))]);
        resBox.scrollIntoView({ behavior: 'smooth', block: 'start' });
      }).catch(function (e) { toast(errText(e), true); });
    }
    Promise.all([call('list_cert_suites'), call('list_cert_targets'), loadSessions()]).then(function (rs) {
      suites = rs[0].result.suites || []; targets = rs[1].result.targets || [];
      var modes = function (m) { return Object.keys(m || {}).sort().map(function (k) { return m[k] + ' ' + k; }).join(', '); };
      add(clear(catBox), [
        table(['Suite', 'FIX', 'Title', 'Cases', 'Modes', 'File'], suites.map(function (x) {
          return el('tr', td(el('strong', x.suite)), td(x.fix_version), td(x.title), td(x.cases, 'num'), td(el('span.small', modes(x.modes))), td(el('span.mono.small', x.file)));
        })),
        el('div', { 'class': 'spacer' }),
        table(['Target', 'Name', 'Control API', 'N/A cases', 'File'], targets.map(function (x) {
          return el('tr', td(el('strong', x.target)), td(x.name), td(x.control_api ? el('span.mono.small', x.control_api) : 'none (assisted cases need a human)'), td(x.not_applicable, 'num'), td(el('span.mono.small', x.file)));
        }))]);
      renderStart();
    });
    loadRuns();
    on('cert', function (info) {
      progress[info.run_id] = info;
      renderProgress();
      if (info.state !== 'running') { loadRuns(); }
    });
    on('refresh', loadRuns);
  }

  // ------------------------------------------------------------ Emulator

  function emulator() {
    var a = state.about;
    if (!a || !a.control_api) { add(clear(app), [el('h1', 'Emulator'), el('p.notice', 'No emulator control API is configured (emulator.control_api in the agent config).')]); return; }
    var emuSessions = Object.keys(a.emulator_sessions || {}).sort();
    var orderBox = el('div');
    var oid = el('input', { type: 'text', placeholder: 'emulator OrderID (tag 37)' });
    var qty = el('input', { type: 'number', min: 1, step: 1, value: 100 });
    var px = el('input', { type: 'text', placeholder: 'optional' });
    var injSess = select('session', emuSessions), injType = el('input', { type: 'text', placeholder: 'e.g. 8 (optional)' });
    var injSet = el('textarea', { placeholder: 'tag=value per line, e.g. 58=altered' }), injRemove = el('input', { type: 'text', placeholder: 'tags, e.g. 151,6' });
    var gapSess = select('session', emuSessions), gapSkip = el('input', { type: 'number', min: 1, max: 100, value: 2 });
    add(clear(app), [el('h1', 'Emulator'),
      el('p.notice.emulator', 'Everything on this page acts on the counterparty EMULATOR (' + a.control_api + '), not on a real venue.'),
      card('Working orders', orderBox),
      card('Act on an order', field('OrderID', oid), field('Quantity', qty), field('Price', px), el('div.row',
        el('button.primary', { type: 'button', onclick: function () { var args = { order_id: oid.value.trim(), qty: qty.value }; if (px.value.trim()) args.price = px.value.trim(); act(call('emulator_fill_order', args), loadOrders); } }, 'Fill'),
        el('button', { type: 'button', onclick: function () { act(call('emulator_hold_order', { order_id: oid.value.trim() }), loadOrders); } }, 'Hold'),
        el('button.danger', { type: 'button', onclick: function () { act(call('emulator_cancel_order', { order_id: oid.value.trim() }), loadOrders); } }, 'Cancel (unsolicited)'))),
      el('div.grid2',
        card('Alter its next message', field('Session', injSess), field('Only MsgType', injType), el('div', el('label', 'Set'), injSet), field('Remove tags', injRemove),
          el('button', { type: 'button', onclick: function () {
            var set = {};
            injSet.value.split('\n').forEach(function (l) { var kv = l.split('='); if (kv.length >= 2 && kv[0].trim()) set[kv[0].trim()] = kv.slice(1).join('='); });
            var args = { session: injSess.value };
            if (injType.value.trim()) args.msg_type = injType.value.trim();
            if (Object.keys(set).length) args.set = set;
            var rm = injRemove.value.split(',').map(function (x) { return x.trim(); }).filter(Boolean);
            if (rm.length) args.remove = rm;
            act(call('emulator_inject_next', args));
          } }, 'Inject next')),
        card('Sequence gap', field('Session', gapSess), field('Skip', gapSkip),
          el('p.small.muted', 'The emulator skips outbound sequence numbers; the agent must ask for a resend.'),
          el('button', { type: 'button', onclick: function () { act(call('emulator_inject_seq_gap', { session: gapSess.value, skip: Number(gapSkip.value) })); } }, 'Skip seqnums')))]);
    function loadOrders() {
      return Promise.all(emuSessions.map(function (id) {
        return call('list_orders', { session_id: id, status: 'open' }).then(function (r) { return (r.result.orders || []).map(function (o) { o.session_id = id; return o; }); }).catch(function () { return []; });
      })).then(function (lists) {
        var rows = [].concat.apply([], lists).map(function (o) {
          return el('tr.clickable', { onclick: function () { oid.value = o.order_id; qty.value = o.leaves_qty; } },
            td(o.session_id), td(el('span.mono.small', o.order_id)), td(el('span.mono.small', o.cl_ord_id)), td(o.symbol), td(o.side), td(o.order_qty, 'num'),
            td(o.price, 'num'), td(badge(o.state)), td(o.cum_qty, 'num'), td(o.leaves_qty, 'num'));
        });
        clear(orderBox).appendChild(rows.length ? table(['Session', 'OrderID', 'ClOrdID', 'Symbol', 'Side', 'Qty', 'Price', 'State', 'Cum', 'Leaves'], rows)
          : el('p.muted', 'No working orders on emulator sessions. (Click a row to pick it.)'));
      });
    }
    loadOrders();
    on('order', debounce(loadOrders, 300));
  }

  // ------------------------------------------------------------ About

  function about() {
    var a = state.about || {};
    get('/api/v1/health').then(function (h) {
      var paths = Object.keys(a.paths || {}).sort().map(function (k) { return [el('dt', k), el('dd', el('span.mono.small', a.paths[k]))]; });
      add(clear(app), [el('h1', 'About'),
        card('Agent', el('dl.kv', el('dt', 'Version'), el('dd', a.version + ' (build ' + a.build + ')'), el('dt', 'PID'), el('dd', h.pid),
          el('dt', 'Service'), el('dd', a.service_url), el('dt', 'Config'), el('dd', el('span.mono.small', a.config)),
          el('dt', 'Working directory'), el('dd', el('span.mono.small', a.work_dir)), el('dt', 'Engine log'), el('dd', el('span.mono.small', a.engine_log)),
          el('dt', 'Service evidence'), el('dd', el('span.mono.small', a.service_evidence)),
          el('dt', 'Emulator control API'), el('dd', a.control_api || 'not configured'))),
        card('Data and logs', el('dl.kv', paths)),
        card('MCP (Claude Desktop)', el('p.small', a.mcp_tools + ' MCP tools are enabled under this config. To connect Claude Desktop, run:'),
          el('pre.raw', a.mcp_hint), el('p.small.muted', 'MCP over HTTP: orderecho serve --mcp-http with a bearer token (mcp.http_token or ORDERECHO_MCP_TOKEN).'))]);
    });
  }

  // ------------------------------------------------------------ boot

  var pages = { dashboard: dashboard, orders: orders, messages: messages, certifications: certifications, emulator: emulator, about: about };
  document.querySelectorAll('.nav a').forEach(function (a) { if (a.getAttribute('data-page') === PAGE) a.className = 'active'; });
  get('/api/v1/about').then(function (a) {
    state.about = a;
    if (a.control_api) document.getElementById('nav-emulator').classList.remove('hidden');
  }).catch(function () {}).then(function () {
    (pages[PAGE] || dashboard)();
    startEvents();
  });
})();
