// Small chart drawer for the head-manager pages. Reads the numbers from a
// <script type="application/json"> next to each chart and draws SVG:
// bars for this period, a dashed line for the period before, a hover box
// with exact numbers, and arrow-key support. Redraws when the screen resizes.
(function () {
  'use strict';
  var NS = 'http://www.w3.org/2000/svg';
  var reduceMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  function el(name, attrs, parent) {
    var n = document.createElementNS(NS, name);
    for (var k in attrs) n.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(n);
    return n;
  }
  function fmt(v, unit, axis) {
    if (v == null) return '–';
    if (unit === 'eur') {
      if (axis) return (v >= 1000 ? (v / 1000).toFixed(v >= 10000 ? 0 : 1).replace('.', ',') + ' k' : Math.round(v)) + ' €';
      return v.toFixed(2).replace('.', ',').replace(/\B(?=(\d{3})+(?!\d))/g, ' ') + ' €';
    }
    return String(Math.round(v));
  }
  function niceMax(max, unit) {
    if (max <= 0) return unit === 'eur' ? 100 : 4;
    var exp = Math.pow(10, Math.floor(Math.log10(max)));
    var steps = [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10];
    for (var i = 0; i < steps.length; i++) {
      var m = steps[i] * exp;
      if (m >= max) {
        if (unit !== 'eur' && m < 4) return 4;
        return m;
      }
    }
    return 10 * exp;
  }

  function draw(box, data) {
    box.innerHTML = '';
    var W = Math.max(280, box.clientWidth);
    var H = box.classList.contains('chart-sm') ? 190 : (W < 560 ? 220 : 280);
    var m = { t: 12, r: 8, b: 26, l: data.unit === 'eur' ? 54 : 30 };
    var iw = W - m.l - m.r, ih = H - m.t - m.b;
    var n = data.labels.length;
    var max = 0;
    data.series.forEach(function (s) { s.values.forEach(function (v) { if (v != null && v > max) max = v; }); });
    var top = niceMax(max, data.unit);
    var y = function (v) { return m.t + ih - (v / top) * ih; };
    var band = iw / n;
    var barW = Math.max(3, Math.min(34, band * 0.68));
    var cx = function (i) { return m.l + band * i + band / 2; };

    var svg = el('svg', { viewBox: '0 0 ' + W + ' ' + H, role: 'img', 'aria-label': box.getAttribute('aria-label') || '' }, box);
    // Grid and y axis
    var grid = el('g', { 'class': 'grid' }, svg), axis = el('g', { 'class': 'axis' }, svg);
    var ticks = 4;
    for (var t = 0; t <= ticks; t++) {
      var v = top / ticks * t, yy = Math.round(y(v)) + .5;
      el('line', { x1: m.l, x2: W - m.r, y1: yy, y2: yy }, grid);
      var tx = el('text', { x: m.l - 8, y: yy + 4, 'text-anchor': 'end' }, axis);
      tx.textContent = fmt(v, data.unit, true);
    }
    // X labels: thin them out so they never overlap
    var every = Math.max(1, Math.ceil(n / Math.floor(iw / 30)));
    data.labels.forEach(function (lab, i) {
      var show = i % every === 0 || i === data.highlight;
      if (every > 1 && i !== data.highlight && Math.abs(i - data.highlight) < every && i % every !== 0) show = false;
      if (!show) return;
      var tx = el('text', { x: cx(i), y: H - 6, 'text-anchor': 'middle', 'class': i === data.highlight ? 'hl' : '' }, axis);
      tx.textContent = lab;
    });

    // Hover bands (bigger than the marks, easy to hit)
    var bands = [];
    for (var i = 0; i < n; i++) bands.push(el('rect', { 'class': 'band', x: m.l + band * i, y: m.t, width: band, height: ih }, svg));

    // Bars
    var bars = [];
    var emph = data.emphasise;
    var maxIdx = -1, maxVal = -1;
    data.series.forEach(function (s) {
      if (s.kind !== 'bar') return;
      s.values.forEach(function (v, i) { if (v != null && v > maxVal) { maxVal = v; maxIdx = i; } });
      s.values.forEach(function (v, i) {
        if (v == null) { bars[i] = null; return; }
        var h = Math.max(v > 0 ? 2 : 0, ih * v / top);
        var x = cx(i) - barW / 2, yy = m.t + ih - h, r = Math.min(4, barW / 2, h);
        var d = 'M' + x + ',' + (m.t + ih) + 'V' + (yy + r) + 'Q' + x + ',' + yy + ' ' + (x + r) + ',' + yy +
          'H' + (x + barW - r) + 'Q' + (x + barW) + ',' + yy + ' ' + (x + barW) + ',' + (yy + r) + 'V' + (m.t + ih) + 'Z';
        var color = emph && i !== maxIdx ? 'var(--series-1-soft)' : s.color;
        var p = el('path', { d: d, fill: color, 'class': 'bar' }, svg);
        if (!reduceMotion && !box.dataset.drawn) {
          p.style.transformOrigin = '0 ' + (m.t + ih) + 'px';
          p.animate([{ transform: 'scaleY(0)' }, { transform: 'scaleY(1)' }], { duration: 500, delay: Math.min(i * 12, 300), easing: 'cubic-bezier(.2,.7,.2,1)', fill: 'backwards' });
        }
        bars[i] = p;
      });
    });
    // Lines (the period before)
    var dots = [];
    data.series.forEach(function (s) {
      if (s.kind !== 'line') return;
      var dpath = '', started = false;
      s.values.forEach(function (v, i) {
        if (v == null) { started = false; return; }
        dpath += (started ? 'L' : 'M') + cx(i) + ',' + y(v);
        started = true;
      });
      el('path', { d: dpath, fill: 'none', stroke: s.color, 'stroke-width': 2, 'stroke-dasharray': s.dashed ? '5 4' : '', 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }, svg);
      dots.push({ s: s, c: el('circle', { r: 5, fill: s.color, 'class': 'dot', visibility: 'hidden' }, svg) });
    });

    // Tooltip
    var tip = document.createElement('div');
    tip.className = 'chart-tip';
    tip.setAttribute('role', 'status');
    box.appendChild(tip);
    var current = -1;
    function show(i) {
      if (i < 0 || i >= n) return;
      current = i;
      box.classList.add('has-focus');
      bands.forEach(function (b, j) { b.classList.toggle('on', j === i); });
      bars.forEach(function (b, j) { if (b) b.classList.toggle('on', j === i); });
      dots.forEach(function (d) {
        var v = d.s.values[i];
        if (v == null) { d.c.setAttribute('visibility', 'hidden'); return; }
        d.c.setAttribute('cx', cx(i)); d.c.setAttribute('cy', y(v)); d.c.setAttribute('visibility', 'visible');
      });
      var html = '<b></b>';
      tip.innerHTML = html;
      tip.firstChild.textContent = (data.long && data.long[i]) || data.labels[i];
      data.series.forEach(function (s) {
        var r = document.createElement('div');
        r.className = 'r';
        r.innerHTML = '<span><i></i></span><strong></strong>';
        r.querySelector('i').style.background = s.color;
        r.querySelector('span').appendChild(document.createTextNode(s.name));
        r.querySelector('strong').textContent = fmt(s.values[i], data.unit);
        tip.appendChild(r);
        if (s.extra && s.extra[i] && s.values[i] != null) s.extra[i].forEach(function (x) {
          var e = document.createElement('div'); e.className = 'x'; e.textContent = x; tip.appendChild(e);
        });
      });
      tip.classList.add('show');
      var tw = tip.offsetWidth, th = tip.offsetHeight;
      var left = (cx(i) / W) * box.clientWidth - tw / 2;
      left = Math.max(0, Math.min(box.clientWidth - tw, left));
      var topPx = Math.max(0, (m.t / H) * box.clientHeight - th - 4);
      tip.style.left = left + 'px';
      tip.style.top = topPx + 'px';
    }
    function hide() {
      current = -1;
      box.classList.remove('has-focus');
      tip.classList.remove('show');
      bands.forEach(function (b) { b.classList.remove('on'); });
      bars.forEach(function (b) { if (b) b.classList.remove('on'); });
      dots.forEach(function (d) { d.c.setAttribute('visibility', 'hidden'); });
    }
    function indexAt(clientX) {
      var r = svg.getBoundingClientRect();
      var x = (clientX - r.left) / r.width * W;
      return Math.max(0, Math.min(n - 1, Math.floor((x - m.l) / band)));
    }
    svg.addEventListener('pointermove', function (e) { show(indexAt(e.clientX)); });
    svg.addEventListener('pointerdown', function (e) { show(indexAt(e.clientX)); });
    svg.addEventListener('pointerleave', function (e) { if (e.pointerType === 'mouse') hide(); });
    box.tabIndex = 0;
    box.onkeydown = function (e) {
      if (e.key === 'ArrowRight') { show(current < 0 ? 0 : Math.min(n - 1, current + 1)); e.preventDefault(); }
      if (e.key === 'ArrowLeft') { show(current < 0 ? n - 1 : Math.max(0, current - 1)); e.preventDefault(); }
      if (e.key === 'Escape') hide();
    };
    box.onblur = hide;
    box.dataset.drawn = '1';
  }

  var charts = Array.prototype.slice.call(document.querySelectorAll('.chart[data-src]'));
  charts.forEach(function (box) {
    var src = document.getElementById(box.dataset.src);
    if (!src) return;
    var data = JSON.parse(src.textContent);
    draw(box, data);
    if ('ResizeObserver' in window) {
      var w = box.clientWidth, timer;
      new ResizeObserver(function () {
        if (Math.abs(box.clientWidth - w) < 4) return;
        w = box.clientWidth;
        clearTimeout(timer);
        timer = setTimeout(function () { draw(box, data); }, 120);
      }).observe(box);
    }
  });
})();
