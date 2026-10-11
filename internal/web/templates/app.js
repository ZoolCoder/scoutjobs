// Every page. The dashboard restarts itself when the scoutjobs binary is upgraded; this notices the
// new version and offers a reload, so an open tab does not keep showing the old pages.
(function () {
  var mine = document.documentElement.dataset.version;
  if (!mine || !window.fetch) return;
  var shown = false;

  function check() {
    if (shown || document.hidden) return;
    fetch('/version', { cache: 'no-store', credentials: 'same-origin' }).then(function (res) {
      return res.ok ? res.text() : '';
    }).then(function (v) {
      v = (v || '').trim();
      if (v && v !== mine) show(v);
    }).catch(function () { /* restarting: try again on the next tick */ });
  }

  function show(v) {
    shown = true;
    var bar = document.createElement('div');
    bar.className = 'upgraded';
    bar.setAttribute('role', 'status');
    var text = document.createElement('span');
    text.textContent = 'ScoutJobs was updated to v' + v + '.';
    var btn = document.createElement('button');
    btn.className = 'mini primary';
    btn.textContent = 'Reload';
    btn.addEventListener('click', function () { location.reload(); });
    bar.appendChild(text);
    bar.appendChild(btn);
    document.body.appendChild(bar);
  }

  setInterval(check, 30000);
  document.addEventListener('visibilitychange', check);
})();

// Job and Profile pages: while an agent run is going, show its progress, how long it has run and
// when it last did something, so the user sees it is alive; reload when it ends.
(function () {
  var panel = document.querySelector('[data-run]');
  if (!panel || !window.fetch) return;
  var url = panel.dataset.run;
  var prog = panel.querySelector('.runprog');
  var last = prog && prog.dataset.last ? Date.parse(prog.dataset.last) : 0;

  function span(ms) {
    var s = Math.max(0, Math.round(ms / 1000));
    if (s < 60) return s + 's';
    var m = Math.floor(s / 60);
    return m < 60 ? m + 'm ' + (s % 60) + 's' : Math.floor(m / 60) + 'h ' + (m % 60) + 'm';
  }

  function set(sel, text) {
    var el = prog && prog.querySelector(sel);
    if (el) el.textContent = text;
  }

  // every second: the clock keeps moving even between polls
  function clock() {
    if (!prog) return;
    var started = Date.parse(prog.dataset.started);
    var ended = prog.dataset.ended ? Date.parse(prog.dataset.ended) : Date.now();
    if (started) set('[data-elapsed]', span(ended - started) + (prog.dataset.ended ? '' : ' elapsed'));
    if (last && !prog.dataset.ended) {
      var quiet = Date.now() - last;
      prog.classList.toggle('quiet', quiet > 120000);
      set('[data-ago]', quiet < 5000 ? 'working now' : quiet > 120000
        ? 'no output for ' + span(quiet) + ', the agent may be thinking; cancel if it stays stuck'
        : 'last output ' + span(quiet) + ' ago');
    }
  }

  function tick() {
    fetch(url, { cache: 'no-store', credentials: 'same-origin' }).then(function (res) {
      return res.ok ? res.json() : null;
    }).then(function (run) {
      if (!run) return;
      if (panel.dataset.queued) { if (run.state !== 'queued') location.reload(); return; }
      if (run.state !== 'running') { location.reload(); return; }
      var done = 0, now = [];
      (run.steps || []).forEach(function (s) {
        if (s.state !== 'pending' && s.state !== 'running') done++;
        if (s.state === 'running') now.push(s.label);
        var li = panel.querySelector('[data-step="' + s.name + '"]');
        if (!li) return;
        var badge = li.querySelector('.badge');
        badge.className = 'badge ' + s.state;
        badge.textContent = s.state;
      });
      if (prog) {
        var bar = prog.querySelector('progress');
        if (bar) bar.value = done;
        set('[data-count]', done + '/' + (run.steps || []).length);
        set('[data-now]', now.length ? now.join(' + ') : 'Starting');
      }
      if (run.last) last = Date.parse(run.last);
      clock();
      var pre = panel.querySelector('details.runlog pre');
      if (pre && run.log) {
        pre.textContent = run.log.slice(-60).join('\n') + '\n';
        pre.scrollTop = pre.scrollHeight;
      }
    }).catch(function () { /* next tick */ });
  }

  clock();
  setInterval(clock, 1000);
  setInterval(tick, 2000);
})();

// Run and prepare buttons: answer the click at once, so a slow start does not look like nothing happened.
(function () {
  document.querySelectorAll('form.runform, form[data-busy]').forEach(function (f) {
    f.addEventListener('submit', function (e) {
      var btn = e.submitter || f.querySelector('button[type=submit], button:not([type])');
      if (!btn) return;
      setTimeout(function () { btn.disabled = true; }, 0);
      btn.classList.add('busy');
      btn.textContent = btn.dataset.busy || f.dataset.busy || 'Starting…';
    });
  });
})();

// Search results: the tag bar narrows the rows to those carrying every chosen tag.
(function () {
  var bar = document.querySelector('[data-tagbar]');
  if (!bar) return;
  var rows = document.querySelectorAll('table.results tbody tr');
  var clear = bar.querySelector('[data-tag-clear]');
  var none = bar.querySelector('[data-tag-none]');
  var count = bar.closest('section').querySelector('.panel-h .n');

  function apply() {
    var on = [];
    bar.querySelectorAll('[data-tag][aria-pressed="true"]').forEach(function (b) { on.push('|' + b.dataset.tag + '|'); });
    var shown = 0;
    rows.forEach(function (tr) {
      var tags = tr.dataset.tags || '';
      var ok = on.every(function (t) { return tags.indexOf(t) >= 0; });
      tr.hidden = !ok;
      if (ok) shown++;
    });
    clear.hidden = on.length === 0;
    none.hidden = shown > 0;
    if (count) count.textContent = on.length ? shown + ' of ' + rows.length : rows.length;
  }

  bar.addEventListener('click', function (e) {
    var b = e.target.closest('button');
    if (!b) return;
    if (b === clear) {
      bar.querySelectorAll('[data-tag]').forEach(function (x) { x.setAttribute('aria-pressed', 'false'); });
    } else if (b.dataset.tag) {
      b.setAttribute('aria-pressed', b.getAttribute('aria-pressed') === 'true' ? 'false' : 'true');
    }
    apply();
  });
})();

// The Connect Google wizard: reload when Google's client download lands, so the page offers to import it.
(function () {
  var box = document.querySelector('[data-gmail-detect]');
  if (!box || box.querySelector('form')) return;
  var tries = 0;
  var timer = setInterval(function () {
    if (++tries > 200) return clearInterval(timer); // ten minutes
    fetch('/gmail/detect', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (d) {
      if (d.file || d.client) { clearInterval(timer); location.reload(); }
    }).catch(function () {});
  }, 3000);
})();
