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
      if (run.state !== 'running') {
        // a link that only changes the #hash does not reload, so reload when the page stays the same
        var done = panel.dataset.done && new URL(panel.dataset.done, location.href);
        if (done && done.pathname + done.search !== location.pathname + location.search) location.href = done.href;
        else location.reload();
        return;
      }
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
      if (panel.dataset.phases) phase(run.log || []);
      if (run.last) last = Date.parse(run.last);
      clock();
      var pre = panel.querySelector('details.runlog pre');
      if (pre && run.log) {
        pre.textContent = run.log.slice(-60).join('\n') + '\n';
        pre.scrollTop = pre.scrollHeight;
      }
    }).catch(function () { /* next tick */ });
  }

  // mail and calendar sync: the agent's tool calls tell how far it got
  var phaseSets = {
    calendar: [
      [/interview_add|interview_set/, 'Saving new interviews…', 85],
      [/create|update|insert/i, 'Adding interviews to your calendar…', 70],
      [/calendar|event/i, 'Reading your calendar…', 45],
      [/interview_list|job_list/, 'Reading your interviews…', 25]
    ],
    mail: [
      [/interview_add|interview_set/, 'Saving interviews…', 90],
      [/email_log|job_set/, 'Logging the replies…', 75],
      [/get_thread|get_message|read_/i, 'Reading the replies…', 60],
      [/search/i, 'Searching your mail…', 40],
      [/job_list/, 'Reading your applications…', 20]
    ]
  };
  var phases = phaseSets[panel.dataset.phases] || phaseSets.calendar;
  function phase(log) {
    var text = 'Starting your AI assistant…', pct = 5;
    log.forEach(function (line) {
      for (var i = 0; i < phases.length; i++) {
        if (phases[i][0].test(line) && phases[i][2] > pct) { text = phases[i][1]; pct = phases[i][2]; break; }
      }
    });
    var bar = prog && prog.querySelector('progress');
    if (bar) bar.value = pct;
    set('[data-now]', text);
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
  var rows = document.querySelectorAll('ul.results > li');
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
  var giveUp = function (text) {
    clearInterval(timer);
    box.querySelector('p').textContent = text;
    var manual = document.querySelector('#google details.manual');
    if (manual) manual.open = true;
  };
  var timer = setInterval(function () {
    if (++tries > 200) return giveUp('No client file arrived in Downloads. Add it below.'); // ten minutes
    fetch('/gmail/detect', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (d) {
      if (d.blind) return giveUp('ScoutJobs cannot look in Downloads (macOS privacy). Add the file below.');
      if (d.file || d.client) { clearInterval(timer); location.reload(); }
    }).catch(function () {});
  }, 3000);
})();

// Inbox › Connect and Connections: show which mail and calendar connectors each AI agent can reach.
(function () {
  var rows = document.querySelectorAll('[data-mail-check]');
  if (!rows.length) return;
  var add = function (parent, tag, cls, text) {
    var el = document.createElement(tag);
    if (cls) el.className = cls;
    el.textContent = text;
    parent.appendChild(el);
    return el;
  };
  var check = function (row, fresh) {
    var out = row.querySelector('.conns');
    out.textContent = fresh ? 'checking again…' : 'looking for mail connectors…';
    fetch(row.dataset.mailCheck + (fresh ? '&fresh=1' : ''), { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (d) {
      out.textContent = '';
      if (d.error) return add(out, 'span', 'auth', d.error);
      var list = (d.connectors || []);
      if (!list.length) add(out, 'span', '', 'no mail connector yet');
      list.forEach(function (c, i) {
        if (i) out.appendChild(document.createTextNode(', '));
        add(out, 'span', c.ready ? 'ready' : 'auth', c.name + (c.ready ? ' ✓' : ' (sign in)'));
      });
      if (d.hint) add(out, 'span', 'hint', d.hint);
    }).catch(function () { out.textContent = 'could not ask the agent'; });
  };
  rows.forEach(function (row) {
    if (!('lazy' in row.dataset)) check(row, false); // on Connections, only when Test is pressed
    var again = row.querySelector('[data-recheck]');
    if (again) again.addEventListener('click', function () { check(row, true); });
  });
})();

// Inbox › Connect: the option cards open their setup details.
document.querySelectorAll('[data-open]').forEach(function (a) {
  var d = document.getElementById(a.dataset.open);
  if (!d) return;
  if (!d.open) d.classList.add('folded');
  a.addEventListener('click', function (e) {
    e.preventDefault();
    d.classList.remove('folded');
    d.open = true;
    d.scrollIntoView({ behavior: 'smooth', block: 'start' });
  });
});

// Job page: a link to a folded section opens it.
(function () {
  var open = function () {
    var d = location.hash && document.getElementById(location.hash.slice(1));
    if (d && d.tagName === 'DETAILS') d.open = true;
  };
  open();
  window.addEventListener('hashchange', open);
})();

// Every page: a thin bar at the top while the next page loads, and the clicked button spins and
// stays disabled, so a slow answer never looks like nothing happened and nothing is sent twice.
(function () {
  var bar = document.createElement('div');
  bar.className = 'loadbar';
  bar.setAttribute('role', 'progressbar');
  bar.setAttribute('aria-label', 'Loading');
  bar.hidden = true;
  document.body.appendChild(bar);
  var delay, safety;

  // fast answers skip the bar; a download never unloads the page, so give up after a while
  function start() {
    clearTimeout(delay);
    clearTimeout(safety);
    delay = setTimeout(function () {
      bar.hidden = false;
      document.body.setAttribute('aria-busy', 'true');
    }, 150);
    safety = setTimeout(stop, 15000);
  }

  function stop() {
    clearTimeout(delay);
    clearTimeout(safety);
    bar.hidden = true;
    document.body.removeAttribute('aria-busy');
    document.querySelectorAll('button.working').forEach(function (b) {
      b.classList.remove('working');
      b.disabled = false;
    });
  }

  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (e.defaultPrevented || (f.target && f.target !== '_self')) return;
    start();
    var btn = e.submitter;
    if (!btn || btn.classList.contains('busy')) return;
    btn.classList.add('working');
    setTimeout(function () { btn.disabled = true; }, 0); // after the form has read the button's value
  });

  document.addEventListener('click', function (e) {
    var a = e.target.closest && e.target.closest('a[href]');
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if ((a.target && a.target !== '_self') || a.hasAttribute('download')) return;
    var u = new URL(a.href, location.href);
    if (u.origin !== location.origin) return;
    if (u.pathname === location.pathname && u.search === location.search && u.hash) return;
    start();
  });

  window.addEventListener('pageshow', stop); // back/forward restores the page as it was left
})();

// Dropdowns built on <details>: close on a click outside or on Escape, like a native menu.
(function () {
  var open = function () { return document.querySelectorAll('details.prep-pop[open]'); };
  document.addEventListener('click', function (e) {
    open().forEach(function (d) { if (!d.contains(e.target)) d.open = false; });
  });
  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    open().forEach(function (d) {
      d.open = false;
      d.querySelector('summary').focus();
    });
  });
})();

// Job e-mails: mark the e-mail shown in the pane, the first one when none is picked.
(function () {
  var list = document.querySelector('.maillist');
  if (!list) return;
  function mark() {
    var links = list.querySelectorAll('a');
    var on = list.querySelector('a[href="' + location.hash + '"]') || links[0];
    links.forEach(function (a) { a.classList.toggle('on', a === on); });
  }
  window.addEventListener('hashchange', mark);
  mark();
})();

// Blocking dialogs: the page waits behind them; Escape does not close them.
document.querySelectorAll('dialog[data-modal]').forEach(function (d) {
  if (!d.showModal) return;
  if (d.open) d.close();
  d.showModal();
  d.addEventListener('cancel', function (e) { e.preventDefault(); });
});

// Switches that save as soon as they flip.
document.querySelectorAll('[data-autosubmit]').forEach(function (el) {
  el.addEventListener('change', function () { el.form.submit(); });
});

// Connections: Test connection asks the server and says the answer under the card.
document.querySelectorAll('[data-test]').forEach(function (btn) {
  btn.addEventListener('click', function () {
    var out = btn.closest('section').querySelector('[data-test-out]');
    var label = btn.textContent;
    btn.disabled = true;
    btn.textContent = 'Testing…';
    out.hidden = false;
    out.className = 'testout';
    out.textContent = 'Checking, this can take up to a minute…';
    fetch('/connections/test?card=' + encodeURIComponent(btn.dataset.test), { cache: 'no-store', credentials: 'same-origin' })
      .then(function (res) { return res.json(); })
      .then(function (r) { out.className = 'testout ' + (r.ok ? 'ok' : 'bad'); out.textContent = r.text; })
      .catch(function () { out.className = 'testout bad'; out.textContent = 'ScoutJobs did not answer. Is it still running?'; })
      .then(function () { btn.disabled = false; btn.textContent = label; });
  });
});

// Sidebar: fold it to icons and back; a cookie keeps the choice so the server draws it folded next time.
(function () {
  var btn = document.querySelector('[data-fold]');
  var shell = document.querySelector('.shell');
  if (!btn || !shell) return;
  function titles(mini) {
    document.querySelectorAll('.side nav a').forEach(function (a) {
      if (mini) { a.dataset.tip = a.textContent.trim(); a.dataset.tipPos = 'right'; } else a.removeAttribute('data-tip');
    });
  }
  titles(shell.classList.contains('mini'));
  btn.addEventListener('click', function () {
    var mini = shell.classList.toggle('mini');
    var label = mini ? 'Expand menu' : 'Collapse menu';
    btn.setAttribute('aria-expanded', String(!mini));
    btn.setAttribute('aria-label', label);
    btn.dataset.tip = label;
    titles(mini);
    document.cookie = 'side=' + (mini ? 'mini' : 'full') + '; path=/; max-age=31536000; samesite=lax';
  });
})();

// "Show all details": open or close every disclosure the button names.
document.querySelectorAll('[data-expand]').forEach(function (btn) {
  btn.addEventListener('click', function () {
    var all = document.querySelectorAll(btn.dataset.expand);
    var open = btn.dataset.open !== '1';
    all.forEach(function (d) { d.open = open; });
    btn.dataset.open = open ? '1' : '';
    btn.textContent = open ? 'Hide details' : 'Show all details';
  });
});

// Tooltips: the templates write plain title attributes, so the hint is there without the script.
// This swaps them for a tooltip that also opens on keyboard focus, stays while the pointer is on it,
// closes with Escape, and is announced as the control's description.
(function () {
  var tip = document.createElement('div');
  tip.id = 'tip';
  tip.className = 'tip';
  tip.setAttribute('role', 'tooltip');
  tip.hidden = true;
  document.body.appendChild(tip);
  var host = null, timer = 0;
  var interactive = 'a[href],button,input,select,textarea,summary,[tabindex]';

  function adopt(root) {
    root.querySelectorAll('[title]:not(iframe):not(svg *)').forEach(function (el) {
      var text = el.getAttribute('title').trim();
      el.removeAttribute('title');
      if (!text) return;
      el.dataset.tip = text;
      // The title was this control's only name (an icon link or button): keep it as the label.
      if (el.matches(interactive) && !el.getAttribute('aria-label') && !el.innerText.trim()) el.setAttribute('aria-label', text);
      // A plain span cannot take focus, so screen readers get the hint as hidden text instead.
      if (!el.matches(interactive) && el.textContent.indexOf(text) < 0) {
        var sr = document.createElement('span');
        sr.className = 'sr-only';
        sr.textContent = ' (' + text + ')';
        el.appendChild(sr);
      }
    });
  }

  function place() {
    var r = host.getBoundingClientRect(), t = tip.getBoundingClientRect(), gap = 8;
    var vw = document.documentElement.clientWidth, x, y;
    if (host.dataset.tipPos === 'right') {
      x = r.right + gap; y = r.top + (r.height - t.height) / 2;
    } else {
      x = r.left + (r.width - t.width) / 2; y = r.top - t.height - gap;
      if (y < 4) y = r.bottom + gap;
    }
    x = Math.max(4, Math.min(x, vw - t.width - 4));
    tip.style.left = Math.round(x) + 'px';
    tip.style.top = Math.round(y) + 'px';
  }

  function show(el) {
    clearTimeout(timer);
    if (host && host !== el) hide();
    host = el;
    tip.textContent = el.dataset.tip;
    tip.hidden = false;
    place();
    if (el.getAttribute('aria-label') !== el.dataset.tip && el.textContent.trim() !== el.dataset.tip) {
      el.setAttribute('aria-describedby', 'tip');
    }
  }

  function hide() {
    clearTimeout(timer);
    if (host && host.getAttribute('aria-describedby') === 'tip') host.removeAttribute('aria-describedby');
    host = null;
    tip.hidden = true;
  }

  function later(fn, ms) { clearTimeout(timer); timer = setTimeout(fn, ms); }
  function target(e) { return e.target.closest && e.target.closest('[data-tip]'); }

  adopt(document);
  document.addEventListener('mouseover', function (e) {
    if (tip.contains(e.target)) { clearTimeout(timer); return; }
    var el = target(e);
    if (el && el !== host) later(function () { show(el); }, 350);
    else if (el) clearTimeout(timer);
  });
  document.addEventListener('mouseout', function (e) {
    var to = e.relatedTarget;
    if (to && (tip.contains(to) || (host && host.contains(to)))) return;
    if (host || target(e)) later(hide, 120);
  });
  document.addEventListener('focusin', function (e) {
    var el = target(e);
    if (el && e.target.matches(':focus-visible')) show(el); else if (host) hide();
  });
  document.addEventListener('focusout', function () { if (host) hide(); });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && host) hide(); });
  window.addEventListener('scroll', function () { if (host) hide(); }, true);
})();

// Pipeline queue strip: follow the AI assistant through the queued jobs until it is done.
(function () {
  var strip = document.querySelector('[data-queue]');
  if (!strip) return;
  var timer = setInterval(function () {
    fetch('/pipeline/queue', { cache: 'no-store', credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (q) {
      var running = q.running || [];
      if (!running.length && !q.waiting) {
        clearInterval(timer);
        strip.textContent = 'Your assistant finished the queue. Reload to see the drafts.';
        return;
      }
      var text = (running.length ? 'Preparing ' + running.join(', ') : 'Starting the next job') + (q.waiting ? ' · ' + q.waiting + ' waiting' : '') + ' · ';
      strip.textContent = text;
      var a = document.createElement('a');
      a.href = '/activity';
      a.textContent = 'Activity';
      strip.appendChild(a);
    }).catch(function () {});
  }, 4000);
})();

// Command palette (Ctrl/Cmd-K), g-then-key page jumps and the ? shortcut list.
(function () {
  var dlg = document.getElementById('palette');
  var help = document.getElementById('keys');
  if (!dlg || !dlg.showModal) return;
  var input = dlg.querySelector('input');
  var list = dlg.querySelector('ul');
  var mac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  document.querySelectorAll('[data-palkey]').forEach(function (k) { k.textContent = mac ? '⌘K' : 'Ctrl K'; });

  var pages = [];
  document.querySelectorAll('.side nav a[href]').forEach(function (a) {
    var c = a.cloneNode(true);
    c.querySelectorAll('.nb').forEach(function (n) { n.remove(); });
    pages.push({ label: c.textContent.trim(), hint: 'Page', href: a.getAttribute('href') });
  });
  var actions = [
    { label: 'Add job', hint: 'Action', href: '/jobs/new' },
    { label: 'Sync mail', hint: 'Action · reads your inbox', post: '/inbox/sync' },
    { label: 'Run daily searches and drafts now', hint: 'Action · sends nothing', post: '/settings/automation/run' }
  ];
  var jobs = null;
  var shown = [];
  var active = 0;
  var opener = null;

  function loadJobs() {
    if (jobs) return;
    jobs = [];
    fetch('/palette.json', { cache: 'no-store', credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (d) {
      jobs = (d || []).map(function (j) { return { label: j.company + ' · ' + j.title, hint: 'SJ-' + j.id + ' · ' + j.status.replace(/_/g, ' '), href: '/jobs/' + j.id }; });
      render();
    }).catch(function () {});
  }

  // score: every query letter in order; earlier, word-start and back-to-back hits rank higher. -1 is no match.
  function score(text, q) {
    text = text.toLowerCase();
    var s = 0, at = -1;
    for (var i = 0; i < q.length; i++) {
      var k = text.indexOf(q[i], at + 1);
      if (k < 0) return -1;
      s += (k === at + 1 ? 3 : 0) + (k === 0 || text[k - 1] === ' ' ? 2 : 0) - Math.min(k - at, 10) / 10;
      at = k;
    }
    return s;
  }

  function render() {
    var q = input.value.trim().toLowerCase();
    var all = actions.concat(pages, jobs || []);
    if (!q) {
      shown = actions.concat(pages);
    } else {
      shown = all.map(function (it) { return { it: it, s: score(it.label + ' ' + it.hint, q) }; })
        .filter(function (x) { return x.s >= 0; })
        .sort(function (a, b) { return b.s - a.s; })
        .slice(0, 12).map(function (x) { return x.it; });
    }
    active = 0;
    list.textContent = '';
    shown.forEach(function (it, i) {
      var li = document.createElement('li');
      li.id = 'pal-' + i;
      li.setAttribute('role', 'option');
      var b = document.createElement('b');
      b.textContent = it.label;
      var sm = document.createElement('small');
      sm.textContent = it.hint;
      li.appendChild(b);
      li.appendChild(sm);
      li.addEventListener('mousedown', function (e) { e.preventDefault(); go(it); });
      list.appendChild(li);
    });
    if (!shown.length) {
      var none = document.createElement('li');
      none.className = 'none';
      none.textContent = 'Nothing matches';
      list.appendChild(none);
    }
    mark();
  }

  function mark() {
    list.querySelectorAll('[role=option]').forEach(function (li, i) {
      li.setAttribute('aria-selected', i === active ? 'true' : 'false');
      if (i === active) li.scrollIntoView({ block: 'nearest' });
    });
    if (shown.length) input.setAttribute('aria-activedescendant', 'pal-' + active);
    else input.removeAttribute('aria-activedescendant');
  }

  function go(it) {
    if (it.post) {
      var f = document.createElement('form');
      f.method = 'post';
      f.action = it.post;
      document.body.appendChild(f);
      f.submit();
      return;
    }
    location.href = it.href;
  }

  function open() {
    if (dlg.open) return;
    opener = document.activeElement;
    input.value = '';
    loadJobs();
    render();
    dlg.showModal();
    input.focus();
  }

  [dlg, help].forEach(function (d) {
    d.addEventListener('close', function () { if (opener && opener.focus) opener.focus(); });
    d.addEventListener('click', function (e) { if (e.target === d) d.close(); });
    // close on Esc ourselves: Chrome skips the built-in cancel for a dialog opened without a user gesture
    d.addEventListener('keydown', function (e) { if (e.key === 'Escape') { e.preventDefault(); d.close(); } });
  });
  input.addEventListener('input', render);
  input.addEventListener('keydown', function (e) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!shown.length) return;
      active = (active + (e.key === 'ArrowDown' ? 1 : shown.length - 1)) % shown.length;
      mark();
    } else if (e.key === 'Enter' && shown[active]) {
      e.preventDefault();
      go(shown[active]);
    } else if (e.key === '?' && !input.value) {
      e.preventDefault();
      dlg.close();
      help.showModal();
    }
  });
  document.querySelectorAll('[data-palette]').forEach(function (b) { b.addEventListener('click', open); });

  var jumps = { o: '/', p: '/pipeline', i: '/inbox', c: '/calendar', s: '/search' };
  var gAt = 0;
  document.addEventListener('keydown', function (e) {
    if ((e.metaKey || e.ctrlKey) && !e.altKey && (e.key === 'k' || e.key === 'K')) {
      e.preventDefault();
      if (help.open) help.close();
      if (dlg.open) dlg.close(); else open();
      return;
    }
    var el = document.activeElement;
    var typing = el && (/^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) || el.isContentEditable);
    if (typing || e.metaKey || e.ctrlKey || e.altKey || dlg.open || help.open) return;
    if (e.key === '?') {
      e.preventDefault();
      opener = el;
      help.showModal();
    } else if (e.key === 'g') {
      gAt = Date.now();
    } else if (gAt && Date.now() - gAt < 1200 && jumps[e.key]) {
      gAt = 0;
      location.href = jumps[e.key];
    } else {
      gAt = 0;
    }
  });
})();
