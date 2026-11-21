// Qaphi: actualización en vivo de las páginas de estado.
//
// La página indica en data-live la URL de sus datos (JSON ya traducido y
// formateado por el servidor). Este script la consulta cada pocos segundos,
// solo mientras la pestaña está visible, y coloca los valores en los
// elementos marcados con data-field. Sin JavaScript, la página se recarga
// sola cada 30 segundos (<noscript>).
(function () {
  'use strict';

  var root = document.querySelector('[data-live]');
  if (!root || !window.fetch) {
    return;
  }
  var url = root.getAttribute('data-live');
  var INTERVAL = 10000;
  var timer = null;

  // set cambia el texto de un campo y lo resalta brevemente si cambió.
  function set(row, field, value) {
    var el = row.querySelector('[data-field="' + field + '"]');
    if (el && el.textContent !== value) {
      el.textContent = value;
      el.classList.remove('changed');
      void el.offsetWidth; // reinicia la animación
      el.classList.add('changed');
    }
    return el;
  }

  function update(data) {
    var banner = document.querySelector('[data-banner]');
    if (banner) {
      banner.className = 'banner banner-' + data.level;
    }
    var summary = document.querySelector('[data-summary]');
    if (summary && summary.textContent !== data.summary) {
      summary.textContent = data.summary;
    }
    var checks = data.checks || [];
    // Si se han creado o borrado checks, se recarga la página entera.
    if (document.querySelectorAll('tr[data-id]').length !== checks.length) {
      location.reload();
      return;
    }
    for (var i = 0; i < checks.length; i++) {
      var c = checks[i];
      var row = document.querySelector('tr[data-id="' + c.id + '"]');
      if (!row) {
        location.reload();
        return;
      }
      var badge = row.querySelector('[data-field="status"]');
      if (badge && badge.textContent !== c.status_label) {
        badge.className = 'badge badge-' + c.status;
        badge.textContent = c.status_label;
      }
      set(row, 'latency', c.latency);
      var last = set(row, 'last', c.last);
      if (last) {
        last.title = c.last_title;
      }
      set(row, 'message', c.message);
    }
  }

  function schedule() {
    clearTimeout(timer);
    timer = setTimeout(tick, INTERVAL);
  }

  function tick() {
    if (document.hidden) {
      schedule();
      return;
    }
    fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
      .then(function (res) {
        // Sesión caducada: el servidor redirige al login.
        if (res.redirected || res.status === 401 || res.status === 403) {
          location.reload();
          throw new Error('session');
        }
        if (!res.ok) {
          throw new Error('HTTP ' + res.status);
        }
        return res.json();
      })
      .then(update)
      .catch(function () { /* se reintenta en el siguiente ciclo */ })
      .then(schedule);
  }

  document.addEventListener('visibilitychange', function () {
    if (!document.hidden) {
      tick();
    }
  });
  schedule();
})();
