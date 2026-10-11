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

// Job page: while a run prepares the application, follow its steps and log, and reload when it ends.
(function () {
  var panel = document.querySelector('[data-run]');
  if (!panel || !window.fetch) return;
  var url = panel.dataset.run;

  function tick() {
    fetch(url, { cache: 'no-store', credentials: 'same-origin' }).then(function (res) {
      return res.ok ? res.json() : null;
    }).then(function (run) {
      if (!run) return;
      if (run.state !== 'running') { location.reload(); return; }
      (run.steps || []).forEach(function (s) {
        var li = panel.querySelector('[data-step="' + s.name + '"]');
        if (!li) return;
        var badge = li.querySelector('.badge');
        badge.className = 'badge ' + s.state;
        badge.textContent = s.state;
      });
      var pre = panel.querySelector('details.runlog pre');
      if (pre && run.log) {
        pre.textContent = run.log.slice(-60).join('\n') + '\n';
        pre.scrollTop = pre.scrollHeight;
      }
    }).catch(function () { /* next tick */ });
  }

  setInterval(tick, 2000);
})();
