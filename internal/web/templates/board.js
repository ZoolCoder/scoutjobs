// Pipeline board. Every link and form here works without JavaScript: the server renders the board
// and, with ?job=<id>, the job's side panel. This script makes it feel like an app:
//  - clicking a card, a filter or a panel button fetches the next page and swaps the board in place
//    (history is kept, so Back closes the panel)
//  - a card can be dragged onto a stage; the drop posts the same form the move buttons post
//  - "/" focuses search, "f" toggles full screen, Esc closes the panel
//  - full screen (?full=1) hides the app chrome and, when the browser allows, enters real full screen
(function () {
  var root = document.querySelector('.pipe');
  if (!root || !window.fetch || !window.DOMParser) return;

  var live = document.getElementById('board-live');
  function announce(msg) {
    if (!live) return;
    live.textContent = msg;
    clearTimeout(live._t);
    live._t = setTimeout(function () { live.textContent = ''; }, 4000);
  }

  function sameOrigin(url) {
    try { return new URL(url, location.href).origin === location.origin; } catch (_) { return false; }
  }

  // load fetches a page and swaps .pipe; push adds a history entry, replace rewrites the current one.
  function load(url, opts, mode) {
    root.classList.add('loading');
    return fetch(url, Object.assign({ credentials: 'same-origin' }, opts || {})).then(function (res) {
      if (!res.ok) throw new Error(res.status + ' ' + res.statusText);
      var final = res.url || url;
      return res.text().then(function (html) {
        var doc = new DOMParser().parseFromString(html, 'text/html');
        var fresh = doc.querySelector('.pipe');
        if (!fresh || new URL(final).pathname !== '/pipeline') { location.href = final; return; }
        var scroll = scrollState();
        root.replaceWith(fresh);
        root = fresh;
        restoreScroll(scroll);
        if (doc.title) document.title = doc.title;
        var path = new URL(final);
        var rel = path.pathname + path.search;
        if (mode === 'push' && rel !== location.pathname + location.search) history.pushState({ pipe: 1 }, '', rel);
        else if (mode !== 'none') history.replaceState({ pipe: 1 }, '', rel);
        arm();
      });
    }).catch(function (err) {
      announce('Could not load: ' + err.message);
    }).finally(function () { root.classList.remove('loading'); });
  }

  // keep the board's horizontal scroll and the open dialog's scroll across swaps
  function scrollState() {
    var b = root.querySelector('.board'), i = root.querySelector('.issue');
    var body = i && i.querySelector('.issue-grid');
    return { board: b ? b.scrollLeft : 0, issue: body ? body.scrollTop : 0, issueId: i ? i.getAttribute('aria-label') : '' };
  }
  function restoreScroll(s) {
    var b = root.querySelector('.board'), i = root.querySelector('.issue');
    if (b) b.scrollLeft = s.board;
    if (!i) return;
    if (i.getAttribute('aria-label') === s.issueId) {
      i.classList.add('still'); // same job after a move or a note: no open animation
      var body = i.querySelector('.issue-grid');
      if (body) body.scrollTop = s.issue;
    } else {
      var x = i.querySelector('.x');
      if (x) x.focus({ preventScroll: true });
    }
  }

  function post(form, submitter) {
    var body = new URLSearchParams(new FormData(form));
    if (submitter && submitter.name) body.set(submitter.name, submitter.value);
    if (!body.get('back')) body.set('back', location.pathname + location.search);
    return load(form.action, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString()
    }, 'replace');
  }

  document.addEventListener('click', function (e) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var a = e.target.closest('a');
    if (!a || !root.contains(a) || a.target === '_blank' || !sameOrigin(a.href)) return;
    var u = new URL(a.href);
    if (u.pathname !== '/pipeline') return;
    e.preventDefault();
    if (a.hasAttribute('data-full')) browserFullscreen(u.searchParams.has('full'));
    load(u.pathname + u.search, null, 'push');
  });

  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!root.contains(form) || !sameOrigin(form.action)) return;
    var method = (form.method || 'get').toLowerCase();
    if (method === 'get') {
      if (new URL(form.action).pathname !== '/pipeline') return;
      e.preventDefault();
      var q = new URLSearchParams(new FormData(form));
      Array.from(q.keys()).forEach(function (k) { if (!q.get(k)) q.delete(k); });
      var cur = new URLSearchParams(location.search);
      if (cur.get('job')) q.set('job', cur.get('job'));
      load('/pipeline' + (q.toString() ? '?' + q : ''), null, 'push');
      return;
    }
    e.preventDefault();
    post(form, e.submitter).then(function () {
      if (e.submitter && e.submitter.name === 'status') announce('Moved to ' + e.submitter.value.replace(/_/g, ' '));
    });
  });

  // filters apply as soon as a select changes
  document.addEventListener('change', function (e) {
    var sel = e.target;
    if (sel.tagName === 'SELECT' && sel.form && sel.form.classList.contains('toolbar') && root.contains(sel)) {
      sel.form.requestSubmit ? sel.form.requestSubmit() : sel.form.submit();
    }
  });

  // the browser's own full screen follows focus mode; requestFullscreen needs the click, so it runs before the fetch
  var ownFullscreen = false;
  function browserFullscreen(on) {
    var d = document, el = d.documentElement;
    if (on && !d.fullscreenElement && el.requestFullscreen) {
      ownFullscreen = true;
      el.requestFullscreen().catch(function () { ownFullscreen = false; });
    } else if (!on && d.fullscreenElement && ownFullscreen) {
      ownFullscreen = false;
      d.exitFullscreen().catch(function () {});
    }
  }
  // Esc in browser full screen leaves it without a keydown; leave focus mode with it
  document.addEventListener('fullscreenchange', function () {
    if (document.fullscreenElement || !ownFullscreen) return;
    ownFullscreen = false;
    var t = root.classList.contains('full') && root.querySelector('[data-full]');
    if (t) load(t.getAttribute('href'), null, 'push');
  });

  window.addEventListener('popstate', function () {
    if (location.pathname === '/pipeline') load(location.pathname + location.search, null, 'none');
  });

  document.addEventListener('keydown', function (e) {
    var typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement && document.activeElement.tagName);
    if (e.key === '/' && !typing) {
      var q = root.querySelector('.toolbar input[name=q]');
      if (q) { e.preventDefault(); q.focus(); q.select(); }
    } else if ((e.key === 'f' || e.key === 'F') && !typing && !e.metaKey && !e.ctrlKey && !e.altKey && !root.querySelector('.issue')) {
      var t = root.querySelector('[data-full]');
      if (t) { e.preventDefault(); t.click(); }
    } else if (e.key === 'Escape') {
      if (typing) { document.activeElement.blur(); return; }
      var x = root.querySelector('[data-close]');
      if (x) { e.preventDefault(); x.click(); }
    }
  });

  // ---- drag and drop ----
  var dragging = null;
  function arm() {
    root.classList.add('js');
    var board = root.querySelector('.board');
    if (!board || !('draggable' in document.createElement('div'))) return;
    board.querySelectorAll('.jobcard[data-id]').forEach(function (card) {
      card.setAttribute('draggable', 'true');
      card.addEventListener('dragstart', function (e) {
        dragging = card;
        card.classList.add('dragging');
        document.body.classList.add('dragging-card');
        e.dataTransfer.effectAllowed = 'move';
        try { e.dataTransfer.setData('text/plain', card.dataset.id); } catch (_) {}
      });
      card.addEventListener('dragend', function () {
        card.classList.remove('dragging');
        document.body.classList.remove('dragging-card');
        root.querySelectorAll('[data-drop]').forEach(function (t) { t.classList.remove('over'); });
        dragging = null;
      });
    });
    root.querySelectorAll('[data-drop]').forEach(function (t) {
      t.addEventListener('dragover', function (e) {
        if (!dragging || t.dataset.drop === dragging.dataset.status) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        t.classList.add('over');
      });
      t.addEventListener('dragleave', function (e) {
        if (!t.contains(e.relatedTarget)) t.classList.remove('over');
      });
      t.addEventListener('drop', function (e) {
        e.preventDefault();
        t.classList.remove('over');
        if (dragging) move(dragging, t.dataset.drop);
      });
    });
  }

  function move(card, status) {
    var id = card.dataset.id;
    if (!id || !status || status === card.dataset.status) return;
    card.classList.add('moving');
    var body = new URLSearchParams({ status: status, back: location.pathname + location.search });
    load('/jobs/' + encodeURIComponent(id) + '/status', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString()
    }, 'replace').then(function () {
      announce(card.dataset.company + ' moved to ' + status.replace(/_/g, ' '));
    });
  }

  arm();
  var open = root.querySelector('.issue .x');
  if (open) open.focus({ preventScroll: true });
  var sel = root.querySelector('.jobcard.sel');
  if (sel && sel.scrollIntoView) sel.scrollIntoView({ block: 'nearest', inline: 'nearest' });
})();
