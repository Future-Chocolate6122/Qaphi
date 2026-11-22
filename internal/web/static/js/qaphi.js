// Qaphi: páginas de estado en el navegador.
//
// El servidor entrega el modelo de datos de la página en JSON (ya traducido y
// formateado) en la URL de data-live; este script lo consulta cada pocos
// segundos, solo con la pestaña visible, y dibuja los componentes:
// barra de pulsos, gráfica de respuesta, anillo de uptime, mapa de calor de
// 90 días, incidencias, favicon y contador en el título. Sin JavaScript, las
// páginas muestran los datos básicos y se recargan cada 30 segundos.
//
// Todo el código es propio (sin librerías) y respeta prefers-reduced-motion.
(function () {
  'use strict';

  var root = document.querySelector('[data-live]');
  if (!root || !window.fetch) {
    return;
  }
  var SVG = 'http://www.w3.org/2000/svg';
  var INTERVAL = 10000;
  var url = root.getAttribute('data-live');
  var lang = root.getAttribute('data-lang') || 'en';
  var baseTitle = document.title;
  var timer = null;
  var model = null;

  // ------------------------------------------------------------ utilidades

  function $(sel, ctx) { return (ctx || document).querySelector(sel); }
  function $$(sel, ctx) { return Array.prototype.slice.call((ctx || document).querySelectorAll(sel)); }

  function el(tag, attrs, parent) {
    var e = tag.charAt(0) === ':' ? document.createElementNS(SVG, tag.slice(1)) : document.createElement(tag);
    for (var k in attrs || {}) {
      if (k === 'text') {
        e.textContent = attrs[k];
      } else {
        e.setAttribute(k, attrs[k]);
      }
    }
    if (parent) {
      parent.appendChild(e);
    }
    return e;
  }

  function svgTitle(node, text) {
    el(':title', { text: text }, node);
  }

  function setText(node, value) {
    if (node && node.textContent !== value) {
      node.textContent = value;
      node.classList.remove('changed');
      void node.offsetWidth; // reinicia la animación
      node.classList.add('changed');
    }
  }

  function cssVar(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  // ------------------------------------------------------------ barra de pulsos

  // renderBeats dibuja los últimos pulsos que caben en el contenedor: altura
  // según el tiempo de respuesta, los antiguos más tenues y el último late.
  function renderBeats(box, beats) {
    if (!box) {
      return;
    }
    var width = box.clientWidth || 300;
    var step = box.classList.contains('beats-lg') ? 12 : 9; // ancho + hueco
    var max = Math.max(1, Math.floor(width / step));
    var list = (beats || []).slice(-max);
    var frag = document.createDocumentFragment();
    // Huecos a la izquierda si aún no hay pulsos suficientes.
    for (var i = list.length; i < max && i < 200; i++) {
      el('span', { 'class': 'beat beat-empty' }, frag);
    }
    list.forEach(function (b, idx) {
      var s = el('span', { 'class': 'beat st-' + b.s, title: b.t }, frag);
      s.style.height = Math.round(Math.max(0.2, Math.min(1, b.h)) * 100) + '%';
      s.style.opacity = (0.45 + 0.55 * (idx + 1) / list.length).toFixed(2);
      if (idx === list.length - 1) {
        s.classList.add('beat-last');
      }
    });
    box.replaceChildren(frag);
  }

  // ------------------------------------------------------------ gráfica

  function niceMax(v) {
    if (v <= 0) {
      return 10;
    }
    var p = Math.pow(10, Math.floor(Math.log10(v)));
    var n = v / p;
    var nice = n <= 1 ? 1 : n <= 2 ? 2 : n <= 2.5 ? 2.5 : n <= 5 ? 5 : 10;
    return nice * p;
  }

  function fmtMs(v) {
    if (v >= 1000) {
      return (v / 1000).toLocaleString(lang, { maximumFractionDigits: 1 }) + ' s';
    }
    return v.toLocaleString(lang, { maximumFractionDigits: v < 10 ? 1 : 0 }) + ' ms';
  }

  function timeFormatter(spanMs, tz) {
    var opts = spanMs > 2 * 86400000
      ? { day: '2-digit', month: '2-digit' }
      : { hour: '2-digit', minute: '2-digit' };
    try {
      opts.timeZone = tz;
      return new Intl.DateTimeFormat(lang, opts);
    } catch (e) {
      delete opts.timeZone;
      return new Intl.DateTimeFormat(lang, opts);
    }
  }

  // renderChart dibuja la gráfica de tiempo de respuesta en SVG.
  function renderChart(box, data) {
    if (!box) {
      return;
    }
    var pts = data.points || [];
    var noData = $('[data-nodata]');
    if (noData) {
      noData.hidden = pts.length > 0;
    }
    box.replaceChildren();
    if (!pts.length) {
      return;
    }
    var W = Math.max(280, box.clientWidth), H = 220;
    var m = { l: 52, r: 12, t: 12, b: 26 };
    var iw = W - m.l - m.r, ih = H - m.t - m.b;
    var from = data.from, to = data.to;
    var maxV = 0;
    pts.forEach(function (p) { if (p.v > maxV) { maxV = p.v; } });
    var yMax = niceMax(maxV * 1.1);
    var x = function (t) { return m.l + (t - from) / (to - from) * iw; };
    var y = function (v) { return m.t + ih - v / yMax * ih; };

    var svg = el(':svg', { viewBox: '0 0 ' + W + ' ' + H, width: W, height: H, 'class': 'chart-svg', role: 'img' }, box);

    // Rejilla y eje Y.
    for (var i = 0; i <= 4; i++) {
      var v = yMax * i / 4, yy = y(v);
      el(':line', { x1: m.l, x2: W - m.r, y1: yy, y2: yy, 'class': 'grid' }, svg);
      el(':text', { x: m.l - 6, y: yy + 4, 'class': 'axis', 'text-anchor': 'end', text: fmtMs(v) }, svg);
    }
    // Eje X.
    var fmt = timeFormatter(to - from, data.tz);
    var ticks = Math.max(2, Math.min(6, Math.floor(iw / 90)));
    for (i = 0; i <= ticks; i++) {
      var t = from + (to - from) * i / ticks;
      el(':text', { x: x(t), y: H - 6, 'class': 'axis', 'text-anchor': i === 0 ? 'start' : i === ticks ? 'end' : 'middle', text: fmt.format(new Date(t)) }, svg);
    }

    // Franjas de caída y área + línea del tiempo de respuesta (cortada en
    // los huecos sin datos).
    var stepPx = iw / Math.max(pts.length, 1);
    var gapMs = (to - from) / Math.max(pts.length, 1) * 3;
    var segs = [], cur = [], prevT = null;
    pts.forEach(function (p) {
      if (p.s === 'down') {
        el(':rect', { x: x(p.t) - Math.max(1.5, stepPx / 2), y: m.t, width: Math.max(3, stepPx), height: ih, 'class': 'down-band' }, svg);
      }
      if (p.v > 0) {
        if (prevT !== null && p.t - prevT > gapMs && cur.length) {
          segs.push(cur);
          cur = [];
        }
        cur.push(p);
        prevT = p.t;
      } else if (cur.length) {
        segs.push(cur);
        cur = [];
        prevT = null;
      }
    });
    if (cur.length) {
      segs.push(cur);
    }
    segs.forEach(function (seg) {
      var line = seg.map(function (p, k) { return (k ? 'L' : 'M') + x(p.t).toFixed(1) + ',' + y(p.v).toFixed(1); }).join('');
      var area = line + 'L' + x(seg[seg.length - 1].t).toFixed(1) + ',' + (m.t + ih) + 'L' + x(seg[0].t).toFixed(1) + ',' + (m.t + ih) + 'Z';
      el(':path', { d: area, 'class': 'area' }, svg);
      el(':path', { d: line, 'class': 'line' }, svg);
    });
    pts.forEach(function (p) {
      if (p.s === 'degraded' && p.v > 0) {
        el(':circle', { cx: x(p.t), cy: y(p.v), r: 2.5, 'class': 'dot-degraded' }, svg);
      }
    });

    // Interacción: guía vertical y tooltip con el punto más cercano.
    var guide = el(':line', { y1: m.t, y2: m.t + ih, 'class': 'guide', visibility: 'hidden' }, svg);
    var dot = el(':circle', { r: 4, 'class': 'guide-dot', visibility: 'hidden' }, svg);
    var tip = el('div', { 'class': 'chart-tip', hidden: '' }, box);
    var overlay = el(':rect', { x: m.l, y: m.t, width: iw, height: ih, 'class': 'overlay' }, svg);
    function hide() {
      guide.setAttribute('visibility', 'hidden');
      dot.setAttribute('visibility', 'hidden');
      tip.hidden = true;
    }
    function move(ev) {
      var rect = svg.getBoundingClientRect();
      var px = (ev.clientX - rect.left) * (W / rect.width);
      var t = from + (px - m.l) / iw * (to - from);
      var best = null, bd = Infinity;
      pts.forEach(function (p) {
        var d = Math.abs(p.t - t);
        if (d < bd) { bd = d; best = p; }
      });
      if (!best) {
        return hide();
      }
      var bx = x(best.t), by = best.v > 0 ? y(best.v) : m.t + ih;
      guide.setAttribute('x1', bx);
      guide.setAttribute('x2', bx);
      guide.setAttribute('visibility', 'visible');
      dot.setAttribute('cx', bx);
      dot.setAttribute('cy', by);
      dot.setAttribute('class', 'guide-dot st-' + best.s);
      dot.setAttribute('visibility', 'visible');
      tip.textContent = best.tip;
      tip.hidden = false;
      var left = bx / W * rect.width;
      tip.style.left = Math.min(Math.max(left, 70), rect.width - 70) + 'px';
    }
    overlay.addEventListener('pointermove', move);
    overlay.addEventListener('pointerdown', move);
    overlay.addEventListener('pointerleave', hide);
  }

  // ------------------------------------------------------------ anillo de uptime

  function renderRing(box, up) {
    if (!box || !up) {
      return;
    }
    var size = 120, r = 50, c = 2 * Math.PI * r;
    var val = up.value < 0 ? 0 : up.value;
    var svg = el(':svg', { viewBox: '0 0 ' + size + ' ' + size, width: size, height: size, role: 'img', 'aria-label': box.getAttribute('data-label') + ': ' + up.text });
    el(':circle', { cx: 60, cy: 60, r: r, 'class': 'ring-track' }, svg);
    var arc = el(':circle', { cx: 60, cy: 60, r: r, 'class': 'ring-value st-' + up.level, transform: 'rotate(-90 60 60)' }, svg);
    arc.setAttribute('stroke-dasharray', (c * val).toFixed(1) + ' ' + c.toFixed(1));
    el(':text', { x: 60, y: 58, 'text-anchor': 'middle', 'class': 'ring-text', text: up.text }, svg);
    el(':text', { x: 60, y: 76, 'text-anchor': 'middle', 'class': 'ring-label', text: up.label }, svg);
    box.replaceChildren(svg);
  }

  // ------------------------------------------------------------ mapa de calor

  // renderHeatmap dibuja los últimos 90 días en columnas de semanas (lunes
  // arriba), como un calendario.
  function renderHeatmap(box, days) {
    if (!box || !days || !days.length) {
      return;
    }
    var cell = 12, gap = 3;
    var first = new Date(days[0].d + 'T12:00:00Z');
    var offset = (first.getUTCDay() + 6) % 7; // lunes = 0
    var cols = Math.ceil((days.length + offset) / 7);
    var W = cols * (cell + gap), H = 7 * (cell + gap);
    var svg = el(':svg', { viewBox: '0 0 ' + W + ' ' + H, width: W, height: H, 'class': 'heatmap-svg' });
    days.forEach(function (d, i) {
      var k = i + offset;
      var rect = el(':rect', {
        x: Math.floor(k / 7) * (cell + gap), y: (k % 7) * (cell + gap),
        width: cell, height: cell, rx: 2, 'class': 'day st-' + d.level
      }, svg);
      svgTitle(rect, d.tip);
    });
    box.replaceChildren(svg);
  }

  // ------------------------------------------------------------ incidencias

  function renderIncidents(list, full) {
    var body = $('[data-incidents]');
    if (!body) {
      return;
    }
    var card = $('[data-incidents-card]');
    var none = $('[data-no-incidents]');
    if (card) {
      card.hidden = !list.length;
    }
    if (none) {
      none.hidden = list.length > 0;
    }
    var heads = $$('th', body.closest('table')).map(function (th) { return th.textContent; });
    var frag = document.createDocumentFragment();
    list.forEach(function (i) {
      var tr = el('tr', {}, frag);
      var td = el('td', { 'data-label': heads[0] || '' }, tr);
      el('a', { href: '/check/' + i.check_id, text: i.check }, td);
      el('td', { 'data-label': heads[1] || '', text: i.start }, tr);
      td = el('td', { 'data-label': heads[2] || '', text: i.duration + ' ' }, tr);
      if (i.open) {
        el('span', { 'class': 'badge badge-down', text: body.getAttribute('data-open-label') }, td);
      }
      if (full) {
        el('td', { 'data-label': heads[3] || '', 'class': 'muted', text: i.error || '' }, tr);
      }
    });
    body.replaceChildren(frag);
  }

  // ------------------------------------------------------------ favicon y título

  function setFavicon(level, down) {
    var color = cssVar('--status-' + (level === 'degraded' ? 'slow' : level)) || '#8a93a3';
    var svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="7" fill="' + color + '"/></svg>';
    var link = $('link[rel="icon"]');
    if (link) {
      link.setAttribute('href', 'data:image/svg+xml,' + encodeURIComponent(svg));
    }
    document.title = (down > 0 ? '(' + down + ') ' : '') + baseTitle;
  }

  // ------------------------------------------------------------ actualización

  function updateBadge(node, status, label) {
    if (node && node.textContent !== label) {
      node.className = 'badge badge-' + status;
      node.textContent = label;
    }
  }

  function updateHome(data) {
    var banner = $('[data-banner]');
    if (banner) {
      banner.className = 'banner banner-' + data.level;
    }
    setText($('[data-summary]'), data.summary);
    var rows = [];
    (data.groups || []).forEach(function (g) { rows = rows.concat(g.checks); });
    if ($$('[data-id]').length !== rows.length) {
      location.reload(); // se han creado o borrado checks
      return;
    }
    rows.forEach(function (c) {
      var li = $('[data-id="' + c.id + '"]');
      if (!li) {
        location.reload();
        return;
      }
      updateBadge($('[data-field="status"]', li), c.status, c.status_label);
      setText($('[data-field="latency"]', li), c.latency);
      var pill = $('[data-field="uptime"]', li);
      if (pill) {
        pill.className = 'pill pill-' + c.uptime_level;
        setText(pill, c.uptime);
      }
      renderBeats($('[data-beats]', li), c.beats);
    });
    renderIncidents(data.incidents || [], (data.incidents || []).some(function (i) { return 'error' in i; }));
    setFavicon(data.level, data.down);
  }

  function updateCheck(data) {
    updateBadge($('[data-field="status"]'), data.status, data.status_label);
    setText($('[data-field="latency"]'), data.latency);
    setText($('[data-field="avg"]'), data.avg);
    if ('message' in data) {
      setText($('[data-field="message"]'), data.message || '');
    }
    (data.uptimes || []).forEach(function (u, i) {
      var dd = $('[data-uptime="' + i + '"]');
      if (dd) {
        dd.className = 'uptime-' + u.level;
        setText(dd, u.text);
      }
    });
    renderRing($('[data-ring]'), (data.uptimes || [])[0]);
    renderBeats($('[data-beats]'), data.beats);
    renderChart($('[data-chart]'), data);
    renderHeatmap($('[data-heatmap]'), data.heatmap);
    renderIncidents(data.incidents || [], (data.incidents || []).some(function (i) { return 'error' in i; }));
    var level = data.status === 'down' ? 'down' : (data.status === 'pending' || data.status === 'degraded') ? 'degraded' : data.status;
    setFavicon(level, data.status === 'down' ? 1 : 0);
  }

  function update(data) {
    model = data;
    if (data.groups) {
      updateHome(data);
    } else {
      updateCheck(data);
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
        // Sesión caducada o página dejada de publicar: al login.
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

  // Selector de rango de la gráfica, sin recargar la página.
  $$('[data-range]').forEach(function (a) {
    a.addEventListener('click', function (ev) {
      ev.preventDefault();
      var r = a.getAttribute('data-range');
      url = url.replace(/([?&]range=)[^&]*/, '$1' + r);
      $$('[data-range]').forEach(function (b) { b.removeAttribute('aria-current'); });
      a.setAttribute('aria-current', 'true');
      history.replaceState(null, '', '?range=' + r);
      clearTimeout(timer);
      tick();
    });
  });

  // Redibujar al cambiar el tamaño (la barra de pulsos y la gráfica dependen
  // del ancho disponible).
  var resizeTimer = null;
  window.addEventListener('resize', function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () {
      if (model) {
        update(model);
      }
    }, 150);
  });

  document.addEventListener('visibilitychange', function () {
    if (!document.hidden) {
      tick();
    }
  });
  tick(); // primera carga: dibuja los componentes de inmediato
})();
