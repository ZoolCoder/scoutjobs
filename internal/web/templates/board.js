// Pipeline board: drag a card onto a column (or onto the stage bar at the bottom) to change its
// status. The drop posts the same form the buttons post; the server redirects back to the
// pipeline and the returned page replaces the board, so counts, buttons and filters stay right.
// Without JavaScript the buttons on every card still work.
(function () {
  var board = document.querySelector('.board');
  if (!board || !('draggable' in document.createElement('div'))) return;

  var dragging = null;

  function cards() { return board.querySelectorAll('.jobcard[data-id]'); }
  function targets() { return document.querySelectorAll('[data-drop]'); }

  function arm() {
    cards().forEach(function (card) {
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
        targets().forEach(function (t) { t.classList.remove('over'); });
        dragging = null;
      });
    });
    targets().forEach(function (t) {
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
        if (!dragging) return;
        move(dragging, t.dataset.drop);
      });
    });
  }

  function move(card, status) {
    var id = card.dataset.id;
    if (!id || !status || status === card.dataset.status) return;
    card.classList.add('moving');
    var body = new URLSearchParams();
    body.set('status', status);
    body.set('back', location.pathname + location.search);
    fetch('/jobs/' + encodeURIComponent(id) + '/status', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
      credentials: 'same-origin'
    }).then(function (res) {
      if (!res.ok) throw new Error(res.status + ' ' + res.statusText);
      return res.text();
    }).then(function (html) {
      var doc = new DOMParser().parseFromString(html, 'text/html');
      var fresh = doc.querySelector('.board');
      var bar = doc.querySelector('.dropbar');
      if (!fresh) { location.reload(); return; }
      board.replaceWith(fresh);
      board = fresh;
      var oldBar = document.querySelector('.dropbar');
      if (oldBar && bar) oldBar.replaceWith(bar); else if (oldBar) oldBar.remove();
      arm();
      announce(card.dataset.company + ' moved to ' + status.replace(/_/g, ' '));
    }).catch(function (err) {
      card.classList.remove('moving');
      announce('Could not move the job: ' + err.message);
    });
  }

  var live = document.getElementById('board-live');
  function announce(msg) {
    if (!live) return;
    live.textContent = msg;
    clearTimeout(live._t);
    live._t = setTimeout(function () { live.textContent = ''; }, 4000);
  }

  arm();
})();
