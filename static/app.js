// E-Meal Kitchen booking: small enhancements on top of pages that already work
// without JavaScript. Every part checks that its page elements exist first.
(function () {
  'use strict';
  var reduceMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  var $ = function (sel, root) { return (root || document).querySelector(sel); };
  var $$ = function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); };
  function euro(c) { return (c / 100).toFixed(2).replace('.', ',') + ' €'; }
  function smoothTo(el) { if (el) el.scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'start' }); }

  // ---------------------------------------------------------------- toast
  var toast = $('.toast');
  if (toast) {
    setTimeout(function () { toast.classList.add('hide'); }, 3800);
    setTimeout(function () { toast.remove(); }, 4300);
    if (history.replaceState && location.search.indexOf('msg=') > -1) {
      var u = new URL(location.href);
      u.searchParams.delete('msg');
      history.replaceState(null, '', u.pathname + u.search + u.hash);
    }
  }

  // ---------------------------------------------------------------- the manager's "More" menu
  var moreBtn = $('.nav-more-btn'), morePanel = $('#nav-more');
  if (moreBtn && morePanel) {
    var setMore = function (open) {
      morePanel.hidden = !open;
      moreBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
    };
    moreBtn.addEventListener('click', function () { setMore(morePanel.hidden); });
    document.addEventListener('click', function (e) {
      if (!morePanel.hidden && !morePanel.contains(e.target) && !moreBtn.contains(e.target)) setMore(false);
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !morePanel.hidden) { setMore(false); moreBtn.focus(); }
    });
  }

  // ---------------------------------------------------------------- dialogs
  document.addEventListener('click', function (e) {
    var open = e.target.closest('[data-open]');
    if (open) {
      var d = document.getElementById(open.getAttribute('data-open'));
      if (d && d.showModal) { e.preventDefault(); d.showModal(); }
      return;
    }
    var close = e.target.closest('[data-close]');
    if (close) { var dlg = close.closest('dialog'); if (dlg) dlg.close(); return; }
    if (e.target.tagName === 'DIALOG') e.target.close(); // click on the dimmed background
  });

  // "Undo" right after adding: gone when its minute is up.
  $$('[data-hide-after]').forEach(function (el) {
    setTimeout(function () { el.remove(); }, (parseInt(el.getAttribute('data-hide-after'), 10) || 0) * 1000);
  });

  // Styled confirmation instead of the browser's grey popup.
  var confirmDlg = $('#confirm-dialog');
  document.addEventListener('submit', function (e) {
    var form = e.target;
    var msg = form.getAttribute('data-confirm');
    if (msg && confirmDlg && confirmDlg.showModal && !form.dataset.confirmed) {
      e.preventDefault();
      $('#confirm-text', confirmDlg).textContent = msg;
      var ok = $('#confirm-ok', confirmDlg);
      ok.textContent = form.getAttribute('data-confirm-ok') || 'Confirm';
      ok.className = 'btn ' + (form.hasAttribute('data-danger') ? 'btn-danger solid' : 'btn-primary');
      var parent = form.closest('dialog');
      if (parent && parent.open) parent.close();
      confirmDlg.showModal();
      var submitter = e.submitter; // keeps e.g. name="method" value="cash" of the button pressed
      ok.onclick = function () {
        form.dataset.confirmed = '1';
        confirmDlg.close();
        if (form.requestSubmit) form.requestSubmit(submitter && submitter.form === form ? submitter : undefined);
        else form.submit();
      };
      return;
    }
    // Loading state: the button shows a spinner so nobody taps twice.
    var btn = e.submitter || $('button[type="submit"], button:not([type])', form);
    if (btn && !e.defaultPrevented) {
      setTimeout(function () {
        btn.classList.add('is-loading');
        if (btn.dataset.loading) btn.textContent = btn.dataset.loading;
      }, 0);
    }
  }, true);

  // Pages restored from the back button should not keep spinners.
  window.addEventListener('pageshow', function (e) {
    if (e.persisted) $$('.is-loading').forEach(function (b) { b.classList.remove('is-loading'); });
  });

  // ---------------------------------------------------------------- booking page
  var booking = $('#booking');
  if (booking) {
    var sizeInput = $('input[name="size"]', booking);
    var dateInput = $('input[name="date"]', booking);
    var timesBox = $('#times');
    var details = $('#step-details');
    var timeStep = $('#step-time');
    var whenLabel = $('#when-label');
    var picker = $('#other-date');
    var pending = null;

    var sum = {
      date: $$('[data-sum="date"]'), time: $$('[data-sum="time"]'),
      guests: $$('[data-sum="guests"]'), deposit: $$('[data-sum="deposit"]'),
      pay: $$('button[data-pay]')
    };
    function set(list, text, empty) {
      list.forEach(function (el) { el.textContent = text; el.classList.toggle('empty', !!empty); });
    }
    function chosenTime() { var r = $('input[name="time"]:checked', booking); return r ? r.value : ''; }
    function refreshSummary() {
      var t = chosenTime();
      set(sum.time, t || booking.dataset.pickTime, !t);
      sum.pay.forEach(function (b) { b.setAttribute('aria-disabled', t ? 'false' : 'true'); });
      $('#step-guests').classList.add('done');
      $('#step-date').classList.toggle('done', !!dateInput.value);
      timeStep.classList.toggle('done', !!t);
    }
    function revealDetails(scroll) {
      if (details.hasAttribute('data-collapsed')) {
        details.removeAttribute('data-collapsed');
        if (scroll) setTimeout(function () { smoothTo(details); }, 60);
      }
    }
    function setURL() {
      if (!history.replaceState) return;
      var u = new URL(location.href);
      u.searchParams.set('size', sizeInput.value);
      u.searchParams.set('date', dateInput.value);
      history.replaceState(null, '', u.pathname + u.search);
    }
    function loadTimes(date) {
      dateInput.value = date;
      timesBox.classList.add('is-loading');
      if (pending) pending.abort();
      pending = new AbortController();
      var u = new URL(booking.dataset.timesUrl, location.href);
      u.searchParams.set('date', date);
      u.searchParams.set('size', sizeInput.value);
      fetch(u, { signal: pending.signal, headers: { 'Accept': 'text/html' } })
        .then(function (r) { return r.text(); })
        .then(function (html) {
          timesBox.innerHTML = html;
          timesBox.classList.remove('is-loading');
          var w = $('[data-when]', timesBox);
          var when = w ? w.getAttribute('data-when') : '';
          if (whenLabel) whenLabel.textContent = when ? '· ' + when : '';
          set(sum.date, w ? w.getAttribute('data-short') : booking.dataset.pickDay, !w);
          refreshSummary();
          setURL();
        })
        .catch(function (err) { if (err.name !== 'AbortError') location.href = u.href.replace('/times?', '?'); });
    }

    // Guests: no reload needed, every time slot fits 1 to 6 guests.
    $$('[data-size]', booking).forEach(function (a) {
      a.addEventListener('click', function (e) {
        e.preventDefault();
        $$('[data-size]', booking).forEach(function (x) { x.removeAttribute('aria-current'); });
        a.setAttribute('aria-current', 'true');
        sizeInput.value = a.dataset.size;
        set(sum.guests, a.getAttribute('aria-label'));
        set(sum.deposit, euro(+a.dataset.deposit));
        setURL();
      });
    });
    // Days: swap in that day's times without reloading.
    $$('[data-date]', booking).forEach(function (a) {
      a.addEventListener('click', function (e) {
        e.preventDefault();
        $$('[data-date]', booking).forEach(function (x) { x.removeAttribute('aria-current'); });
        a.setAttribute('aria-current', 'date');
        if (picker) picker.value = a.dataset.date;
        loadTimes(a.dataset.date);
      });
    });
    if (picker) picker.addEventListener('change', function () {
      if (!picker.value) return;
      var chip = $('[data-date="' + picker.value + '"]', booking);
      $$('[data-date]', booking).forEach(function (x) { x.removeAttribute('aria-current'); });
      if (chip) chip.setAttribute('aria-current', 'date');
      loadTimes(picker.value);
    });
    timesBox.addEventListener('change', function (e) {
      if (e.target.name === 'time') { refreshSummary(); revealDetails(true); }
    });
    // Pay without a time: point at the time step instead of failing.
    sum.pay.forEach(function (b) {
      b.addEventListener('click', function (e) {
        if (b.getAttribute('aria-disabled') === 'true') {
          e.preventDefault();
          smoothTo(timeStep);
          timeStep.animate && !reduceMotion && timeStep.animate(
            [{ transform: 'translateX(0)' }, { transform: 'translateX(-6px)' }, { transform: 'translateX(6px)' }, { transform: 'translateX(0)' }],
            { duration: 260, delay: 300 });
        }
      });
    });
    // The pay bar steps aside when the pay button at the end of the form is on screen.
    var inlinePay = $('.inline-pay', booking), paybar = $('.paybar', booking);
    if (inlinePay && paybar && 'IntersectionObserver' in window) {
      new IntersectionObserver(function (en) { paybar.classList.toggle('away', en[0].isIntersecting); }).observe(inlinePay);
    }
    // Keep the chosen day in view in the sideways day row.
    var row = $('.days', booking), cur = row && $('[aria-current]', row);
    if (cur) row.scrollLeft = cur.offsetLeft - (row.clientWidth - cur.offsetWidth) / 2;
    // Hide the details until a time is picked (unless the server showed errors).
    if (!chosenTime() && !booking.hasAttribute('data-has-errors')) details.setAttribute('data-collapsed', '');
    refreshSummary();
  }

  // ---------------------------------------------------------------- takeaway
  var order = $('#order');
  if (order) {
    var inputs = $$('input[data-price]', order);
    var bar = $('#cartbar');
    var cartBox = $('#cart-lines');
    var checkoutVisible = false;
    var total = 0;
    var T = order.dataset;

    function syncQty(input) {
      var n = Math.max(0, Math.min(20, parseInt(input.value, 10) || 0));
      if (String(n) !== input.value && input !== document.activeElement) input.value = n;
      var q = input.closest('.qty');
      q.classList.toggle('is-zero', n === 0);
      input.closest('.dish').classList.toggle('picked', n > 0);
      return n;
    }
    function update() {
      var count = 0, lines = [];
      total = 0;
      inputs.forEach(function (i) {
        var n = syncQty(i), price = parseInt(i.dataset.price, 10);
        count += n; total += n * price;
        if (n) lines.push({ name: i.dataset.name, n: n, sum: n * price });
      });
      var deposit = total > 0 ? Math.max(50, Math.round(total * 25 / 100)) : 0;
      $$('[data-sum="total"]').forEach(function (el) { el.textContent = euro(total); });
      $$('[data-sum="deposit"]').forEach(function (el) { el.textContent = euro(deposit); });
      $$('[data-sum="rest"]').forEach(function (el) { el.textContent = euro(total - deposit); });
      $$('[data-sum="count"]').forEach(function (el) { el.textContent = count === 1 ? T.wordOne : T.wordMany.replace('%d', count); });
      $$('button[data-pay]').forEach(function (b) {
        b.textContent = total > 0 ? T.wordPay + ' · ' + euro(deposit) : T.wordPay;
        b.setAttribute('aria-disabled', total > 0 ? 'false' : 'true');
      });
      if (cartBox) {
        cartBox.innerHTML = '';
        lines.forEach(function (l) {
          var li = document.createElement('li');
          li.innerHTML = '<span class="q"></span><span></span><span class="p"></span>';
          li.children[0].textContent = l.n + ' ×';
          li.children[1].textContent = l.name;
          li.children[2].textContent = euro(l.sum);
          cartBox.appendChild(li);
        });
        $('#cart-empty').hidden = lines.length > 0;
      }
      showBar();
    }
    function showBar() { if (bar) bar.hidden = total === 0 || checkoutVisible; }

    order.addEventListener('click', function (e) {
      var add = e.target.closest('.add-btn');
      var step = e.target.closest('.qty-btn');
      if (!add && !step) return;
      var input = (add || step).closest('.qty').querySelector('input');
      var d = add ? 1 : parseInt(step.dataset.step, 10);
      input.value = Math.max(0, Math.min(20, (parseInt(input.value, 10) || 0) + d));
      update();
      if (add) { var plus = input.parentNode.querySelector('[data-step="1"]'); if (plus) plus.focus(); }
    });
    order.addEventListener('input', update);
    $$('button[data-pay]').forEach(function (b) {
      b.addEventListener('click', function (e) {
        if (b.getAttribute('aria-disabled') === 'true') { e.preventDefault(); smoothTo($('#menu-start')); }
      });
    });

    // Pickup day: show that day's times, clear a time picked on another day.
    $$('input[name="date"]', order).forEach(function (r) {
      r.addEventListener('change', function () {
        $$('.pickday', order).forEach(function (d) {
          var on = d.dataset.day === r.value;
          d.hidden = !on;
          if (!on) $$('input[name="time"]', d).forEach(function (t) { t.checked = false; });
        });
      });
    });

    if ('IntersectionObserver' in window) {
      new IntersectionObserver(function (entries) {
        entries.forEach(function (e) { checkoutVisible = e.isIntersecting; });
        showBar();
      }, { rootMargin: '0px 0px -35% 0px' }).observe($('#when'));

      var tabs = $$('.cat-tabs a');
      var catObs = new IntersectionObserver(function (entries) {
        entries.forEach(function (e) {
          if (!e.isIntersecting) return;
          tabs.forEach(function (t) {
            var on = t.dataset.cat === e.target.dataset.cat;
            t.classList.toggle('on', on);
            if (on) t.parentNode.scrollTo({ left: t.offsetLeft - 16, behavior: reduceMotion ? 'auto' : 'smooth' });
          });
        });
      }, { rootMargin: '-20% 0px -70% 0px' });
      $$('h3.cat').forEach(function (h) { catObs.observe(h); });
    }
    update();
  }

  // ---------------------------------------------------------------- manager: filters
  var filterTabs = $('[data-filter-tabs]');
  if (filterTabs) {
    var cards = $$('.bk[data-group]');
    filterTabs.addEventListener('click', function (e) {
      var b = e.target.closest('button[data-filter]');
      if (!b) return;
      $$('button', filterTabs).forEach(function (x) { x.setAttribute('aria-selected', x === b ? 'true' : 'false'); });
      var f = b.dataset.filter;
      var shown = 0;
      cards.forEach(function (c) {
        var on = f === 'all' || c.dataset.group === f;
        c.hidden = !on;
        if (on) shown++;
      });
      var empty = $('#filter-empty');
      if (empty) empty.hidden = shown > 0;
    });
  }
  // Jump from a table tile to its booking and flash it.
  $$('a.tile[href^="#b-"]').forEach(function (a) {
    a.addEventListener('click', function () {
      var t = document.getElementById(a.getAttribute('href').slice(1));
      if (t) { t.hidden = false; t.classList.add('flash'); setTimeout(function () { t.classList.remove('flash'); }, 1600); }
    });
  });

  // A booking staff just added: bring it into view.
  var fresh = $('.bk.flash');
  if (fresh) {
    setTimeout(function () { fresh.scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'center' }); }, 150);
    setTimeout(function () { fresh.classList.remove('flash'); }, 2600);
    if (history.replaceState && location.search.indexOf('hl=') > -1) {
      var hu = new URL(location.href);
      hu.searchParams.delete('hl');
      history.replaceState(null, '', hu.pathname + hu.search + hu.hash);
    }
  }

  // ---------------------------------------------------------------- auto refresh
  var refresh = document.body.getAttribute('data-autorefresh');
  if (refresh) {
    var again = function () {
      var busy = $('dialog[open]') || (document.activeElement && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName));
      if (busy || document.hidden) return setTimeout(again, 15000);
      location.reload();
    };
    setTimeout(again, +refresh * 1000);
  }

  // ---------------------------------------------------------------- live screens
  // Tables, Orders and the Kitchen screen. Each page works without this
  // (forms post and the page reloads). With it: the screen redraws the moment
  // something changes (a button pressed, food ordered, a dish done), new
  // alerts chime and vibrate, and taps don't reload the page.
  //
  //   [data-live-screen="/url?part=..."]  the part of the page to redraw
  //   [data-live-box]                      where it goes
  //   [data-alert-key]                     things that should chime when new
  //   [data-live-pill], [data-sound]       the "Live" label and the sound switch
  //   form[data-live]                      sent in the background
  var liveRoot = $('[data-live-screen]');
  var liveBox = $('[data-live-box]');
  if (liveRoot && liveBox) (function () {
    var src = liveRoot.getAttribute('data-live-screen');
    var eventsURL = liveRoot.getAttribute('data-live-events') || '/admin/events';
    var lastHTML = null;
    var pill = $('[data-live-pill]');
    var soundBtn = $('[data-sound]');
    var baseTitle = document.title;
    var ctx = null, wake = null, soundOn = false;
    var stored = function (k, v) { try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v); } catch (e) { return null; } };

    var alertKeys = function () { return $$('[data-alert-key]', liveBox).map(function (el) { return el.getAttribute('data-alert-key'); }); };
    var seen = {};
    alertKeys().forEach(function (k) { seen[k] = true; });
    var setTitle = function (n) { document.title = n ? '(' + n + ') ' + baseTitle : baseTitle; };
    setTitle(alertKeys().length);

    // ---- sound (browsers only allow it after a tap on the page)
    function tone(freq, start, len, vol) {
      var o = ctx.createOscillator(), g = ctx.createGain(), t = ctx.currentTime + start;
      o.type = 'sine';
      o.frequency.value = freq;
      g.gain.setValueAtTime(0.0001, t);
      g.gain.exponentialRampToValueAtTime(vol, t + 0.02);
      g.gain.exponentialRampToValueAtTime(0.0001, t + len);
      o.connect(g);
      g.connect(ctx.destination);
      o.start(t);
      o.stop(t + len + 0.05);
    }
    function ding(strong) {
      if (!soundOn || !ctx) return;
      if (ctx.state === 'suspended') ctx.resume();
      var v = strong ? 0.6 : 0.3;
      tone(880, 0, 0.25, v);
      tone(1175, 0.22, 0.45, v);
      if (strong) { tone(880, 0.85, 0.25, v); tone(1175, 1.07, 0.45, v); }
    }
    function keepAwake() {
      if (navigator.wakeLock && document.visibilityState === 'visible' && !wake) {
        navigator.wakeLock.request('screen').then(function (w) {
          wake = w;
          w.addEventListener('release', function () { wake = null; });
        }).catch(function () {});
      }
    }
    if (soundBtn) {
      var setSound = function (on) {
        soundOn = on;
        if (on && !ctx) {
          var AC = window.AudioContext || window.webkitAudioContext;
          if (AC) ctx = new AC();
        }
        if (on && ctx && ctx.state === 'suspended') ctx.resume();
        soundBtn.textContent = soundBtn.getAttribute(on ? 'data-text-live' : 'data-text-wait');
        soundBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
        soundBtn.className = 'btn btn-sm ' + (on ? 'btn-ok' : 'btn-secondary');
        stored('ozmo-sound', on ? '1' : '0');
        if (on) keepAwake();
      };
      soundBtn.hidden = false;
      soundBtn.addEventListener('click', function () {
        setSound(!soundOn);
        if (soundOn) ding(false);
      });
      // Sound was on last time: switch it on again at the first tap anywhere.
      if (stored('ozmo-sound') === '1') {
        document.addEventListener('pointerdown', function first(e) {
          document.removeEventListener('pointerdown', first, true);
          if (!soundOn && e.target !== soundBtn) setSound(true);
        }, true);
      }
      document.addEventListener('visibilitychange', function () { if (soundOn) keepAwake(); });
      setInterval(function () { if (alertKeys().length) ding(false); }, 120000); // reminder while something waits
    }

    // ---- redraw, and chime when something new appeared since last time
    var loading = false, again = false;
    function redraw() {
      if (loading) { again = true; return; }
      if ($('dialog[open]', liveBox)) return; // don't pull a dialog away from someone using it
      var typing = document.activeElement;
      if (typing && liveBox.contains(typing) && /INPUT|TEXTAREA|SELECT/.test(typing.tagName)) {
        setTimeout(redraw, 3000); // someone is typing here (e.g. their email): try again shortly
        return;
      }
      loading = true;
      fetch(src, { credentials: 'same-origin', headers: { 'X-Live': '1' } })
        .then(function (res) {
          if (res.redirected && res.url.indexOf('/login') > -1) { location.reload(); return null; }
          return res.ok ? res.text() : null;
        })
        .then(function (html) {
          if (html == null || html === lastHTML) return; // nothing changed here: leave it alone
          lastHTML = html;
          // Open sections (e.g. "Pay for the whole table") stay open.
          var openIds = $$('details[open][id]', liveBox).map(function (d) { return d.id; });
          liveBox.innerHTML = html;
          openIds.forEach(function (id) { var d = document.getElementById(id); if (d) d.open = true; });
          document.dispatchEvent(new CustomEvent('live:redrawn'));
          var keys = alertKeys(), fresh = false, now = {};
          keys.forEach(function (k) { if (!seen[k]) fresh = true; now[k] = true; });
          seen = now;
          setTitle(keys.length);
          if (fresh) {
            ding(true);
            if (navigator.vibrate) navigator.vibrate([250, 120, 250]);
          }
        })
        .catch(function () {})
        .then(function () {
          loading = false;
          if (again) { again = false; redraw(); }
        });
    }
    setInterval(redraw, 30000); // keeps the minutes fresh, and catches anything missed
    document.addEventListener('countdown:end', redraw); // a payment's time ran out
    window.addEventListener('pageshow', function (e) { if (e.persisted) redraw(); }); // back from the payment page

    // ---- live connection: the server says "change" the moment anything happens
    function setLive(on) {
      if (!pill) return;
      pill.hidden = false;
      pill.classList.toggle('off', !on);
      pill.textContent = pill.getAttribute(on ? 'data-text-live' : 'data-text-wait');
    }
    function connect() {
      if (!window.EventSource) return;
      var es = new EventSource(eventsURL);
      es.addEventListener('change', function () { setLive(true); redraw(); });
      es.onopen = function () { setLive(true); };
      es.onerror = function () {
        setLive(false);
        if (es.readyState === 2) { es.close(); setTimeout(connect, 10000); } // gave up: start again later
      };
    }
    connect();

    // ---- taps inside the screen: send without reloading, then redraw
    function note(text) {
      var t = document.createElement('div');
      t.className = 'toast';
      t.setAttribute('role', 'status');
      t.textContent = text;
      document.body.appendChild(t);
      setTimeout(function () { t.classList.add('hide'); }, 3800);
      setTimeout(function () { t.remove(); }, 4300);
    }
    liveBox.addEventListener('submit', function (e) {
      var form = e.target;
      if (!form.hasAttribute('data-live') || e.defaultPrevented) return;
      e.preventDefault();
      var btn = e.submitter || $('button', form);
      if (btn) btn.classList.add('is-loading');
      // Sent: let go of the box typed in (e.g. how many are left), so the
      // screen can redraw with the new value.
      if (document.activeElement && form.contains(document.activeElement)) document.activeElement.blur();
      fetch(form.action, {
        method: 'POST', credentials: 'same-origin', headers: { 'X-Live': '1' },
        body: new URLSearchParams(new FormData(form))
      }).then(function (res) {
        if (res.status === 401 || (res.redirected && res.url.indexOf('/login') > -1)) { location.reload(); return; }
        if (!res.ok) return res.text().then(note);
      }).catch(function () {
        note(liveBox.getAttribute('data-offline') || 'No connection');
      }).then(redraw);
    });
  })();

  // ---------------------------------------------------------------- taking an order at a table
  var staffOrder = $('#staff-order') || $('#guest-order');
  if (staffOrder) {
    var oInputs = $$('input[data-price]', staffOrder);
    var oSum = $('[data-order-sum]', staffOrder);
    var oEmpty = oSum ? oSum.textContent : '';
    var priceOf = function (input) {
      var typed = $('input[data-price-for="' + input.dataset.id + '"]', staffOrder);
      if (typed && typed.value.trim() !== '') {
        var v = parseFloat(typed.value.replace('€', '').replace(',', '.'));
        if (!isNaN(v) && v >= 0) return Math.round(v * 100);
      }
      return parseInt(input.dataset.price, 10);
    };
    var oUpdate = function () {
      var count = 0, total = 0;
      oInputs.forEach(function (i) {
        var n = Math.max(0, Math.min(+i.max || 50, parseInt(i.value, 10) || 0));
        if (String(n) !== i.value && i !== document.activeElement) i.value = n;
        i.closest('.qty').classList.toggle('is-zero', n === 0);
        i.closest('.dish').classList.toggle('picked', n > 0);
        count += n;
        total += n * priceOf(i);
      });
      $$('[data-fav]', staffOrder).forEach(function (b) {
        var i = $('input[name="qty_' + b.dataset.fav + '"]', staffOrder);
        var n = i ? parseInt(i.value, 10) || 0 : 0;
        var badge = $('[data-fav-n]', b);
        badge.hidden = n === 0;
        badge.textContent = n + '×';
        b.classList.toggle('on', n > 0);
      });
      if (oSum) {
        oSum.textContent = count === 0 ? oEmpty
          : (count === 1 ? staffOrder.dataset.one : staffOrder.dataset.many.replace('%d', count)) + ' · ' + euro(total);
      }
    };
    staffOrder.addEventListener('click', function (e) {
      var add = e.target.closest('.add-btn');
      var step = e.target.closest('.qty-btn');
      if (!add && !step) return;
      var input = (add || step).closest('.qty').querySelector('input');
      var d = add ? 1 : parseInt(step.dataset.step, 10);
      input.value = Math.max(0, Math.min(+input.max || 50, (parseInt(input.value, 10) || 0) + d));
      oUpdate();
      if (add) { var plus = input.parentNode.querySelector('[data-step="1"]'); if (plus) plus.focus(); }
    });
    staffOrder.addEventListener('input', oUpdate);
    oUpdate();
    // The big "Most ordered" buttons: one tap, one more.
    staffOrder.addEventListener('click', function (e) {
      var fav = e.target.closest('[data-fav]');
      if (!fav) return;
      var i = $('input[name="qty_' + fav.dataset.fav + '"]', staffOrder);
      if (!i) return;
      i.value = Math.min(+i.max || 50, (parseInt(i.value, 10) || 0) + 1);
      oUpdate();
    });
    // Quick notes for the chef: tap to add or take away. A note on a dish
    // that isn't picked yet picks one.
    staffOrder.addEventListener('click', function (e) {
      var chip = e.target.closest('.note-chip');
      if (!chip) return;
      var note = $('input[name^="note_"]', chip.closest('.od-extra'));
      if (!note) return;
      var parts = note.value.split(',').map(function (x) { return x.trim(); }).filter(Boolean);
      var at = parts.indexOf(chip.dataset.note);
      if (at >= 0) parts.splice(at, 1); else parts.push(chip.dataset.note);
      note.value = parts.join(', ').slice(0, 120);
      chip.setAttribute('aria-pressed', at >= 0 ? 'false' : 'true');
      var qty = $('.qty input', chip.closest('.dish'));
      if (qty && at < 0 && !(parseInt(qty.value, 10) > 0)) { qty.value = 1; oUpdate(); }
    });
    staffOrder.addEventListener('input', function (e) {
      if (!e.target.name || e.target.name.indexOf('note_') !== 0) return;
      var parts = e.target.value.split(',').map(function (x) { return x.trim(); });
      $$('.note-chip', e.target.closest('.od-extra')).forEach(function (c) {
        c.setAttribute('aria-pressed', parts.indexOf(c.dataset.note) >= 0 ? 'true' : 'false');
      });
    });
    // Tap a dish's name or price to add one more (like the old till's keys).
    staffOrder.addEventListener('click', function (e) {
      var tap = e.target.closest('.od-tap');
      if (!tap) return;
      var plus = tap.closest('.dish').querySelector('.qty [data-step="1"]');
      if (plus) plus.click();
    });
  }

  // ---------------------------------------------------------------- "keep it on the menu?" (a special)
  $$('[data-keep]').forEach(function (sel) {
    var box = sel.closest('details') || document;
    var until = $('[data-keep-until]', box), more = $('[data-keep-more]', box);
    var show = function () {
      if (until) until.hidden = sel.value !== 'until';
      if (more) more.hidden = sel.value === '';
    };
    sel.addEventListener('change', show);
    show();
  });

  // ---------------------------------------------------------------- menu sections as big buttons
  // Tap "Förrätter" and only the starters show; "Everything" shows all.
  // Each button counts what's picked from that section. Searching looks in
  // every section. The chosen section is remembered on this device.
  $$('[data-cat-tabs]').forEach(function (tabs) {
    var form = tabs.closest('form');
    var secs = $$('[data-cat-sec]', form);
    var btns = $$('[data-cat]', tabs);
    var search = $('[data-dish-search] input[type=search]', form) || $('[data-dish-search] input', form);
    var nojs = $('.od-jump', form);
    if (!secs.length) return;
    if (nojs) nojs.hidden = true;
    tabs.hidden = false;
    var keep = function (v) { try { if (v === undefined) return localStorage.getItem('ozmo-menu-cat'); localStorage.setItem('ozmo-menu-cat', v); } catch (e) { return null; } };
    var current = keep();
    if (!btns.some(function (b) { return b.dataset.cat === current; })) current = secs[0].dataset.catSec;
    var show = function () {
      var searching = search && search.value.trim() !== '';
      btns.forEach(function (b) { b.setAttribute('aria-pressed', !searching && b.dataset.cat === current ? 'true' : 'false'); });
      secs.forEach(function (sec) { sec.hidden = !searching && current !== 'all' && sec.dataset.catSec !== current; });
    };
    var count = function () {
      secs.forEach(function (sec) {
        var n = 0;
        $$('input[data-price]', sec).forEach(function (i) { n += Math.max(0, parseInt(i.value, 10) || 0); });
        var badge = $('[data-cat-count="' + sec.dataset.catSec + '"]', tabs);
        if (badge) badge.textContent = n ? String(n) : '';
      });
    };
    tabs.addEventListener('click', function (e) {
      var b = e.target.closest('[data-cat]');
      if (!b) return;
      current = b.dataset.cat;
      keep(current);
      if (search && search.value) { search.value = ''; search.dispatchEvent(new Event('input')); }
      show();
    });
    if (search) search.addEventListener('input', show);
    form.addEventListener('input', count);
    form.addEventListener('click', function () { setTimeout(count, 0); });
    show();
    count();
  });

  // ---------------------------------------------------------------- countdowns
  // <b data-countdown="117"> counts down the seconds left (sent as seconds,
  // not a clock time, because a phone's clock can be wrong). At zero, live
  // screens redraw; other pages reload (unless someone is typing).
  var timers = function () { return $$('[data-countdown]'); };
  if (timers().length || $('[data-live-screen]')) {
    var lastEnd = 0;
    var tick = function () {
      timers().forEach(function (el) {
        if (!el._until) el._until = Date.now() + (+el.getAttribute('data-countdown') || 0) * 1000;
        var left = Math.max(0, Math.round((el._until - Date.now()) / 1000));
        el.textContent = Math.floor(left / 60) + ':' + ('0' + (left % 60)).slice(-2);
        if (left > 0 || el._ended || Date.now() - lastEnd < 5000) return;
        el._ended = true;
        lastEnd = Date.now();
        if ($('[data-live-screen]')) {
          setTimeout(function () { document.dispatchEvent(new CustomEvent('countdown:end')); }, 1200);
        } else {
          setTimeout(function () {
            var busy = $('dialog[open]') || (document.activeElement && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName));
            if (!busy) location.reload();
          }, 1500);
        }
      });
    };
    tick();
    setInterval(tick, 1000);
  }

  // ---------------------------------------------------------------- a guest's page at the table
  // The menu hides while the guest can't order (someone is paying for the
  // whole table, or it's paid), and comes back when they can again.
  var guestMenu = $('[data-guest-menu]');
  if ($('[data-menu-state]')) {
    document.addEventListener('live:redrawn', function () {
      var st = $('[data-menu-state]');
      var open = st && st.getAttribute('data-menu-state') === 'open';
      var nameForm = $('#tab-name');
      if (nameForm && st) nameForm.hidden = st.getAttribute('data-bill-open') !== '1';
      if (guestMenu) guestMenu.hidden = !open;
      else if (open) location.reload(); // the waiter just opened the table for orders
    });
  }

  // The guest bill's Print button.
  $$('[data-print]').forEach(function (b) { b.addEventListener('click', function () { window.print(); }); });

  // ---------------------------------------------------------------- QR codes on receipts
  // <div data-qr="https://..."> becomes a QR code (static/qrcode.js, loaded on
  // the pages that have one). Drawn as an SVG, so it prints sharp.
  if (window.qrcode) {
    $$('[data-qr]').forEach(function (el) {
      var q = window.qrcode(0, 'M');
      q.addData(el.getAttribute('data-qr'));
      q.make();
      var n = q.getModuleCount(), d = '';
      for (var r = 0; r < n; r++) {
        for (var c = 0; c < n; c++) {
          if (q.isDark(r, c)) d += 'M' + c + ' ' + r + 'h1v1h-1z';
        }
      }
      var size = n + 8;
      el.innerHTML = '<svg viewBox="-4 -4 ' + size + ' ' + size + '" shape-rendering="crispEdges" role="img">' +
        '<rect x="-4" y="-4" width="' + size + '" height="' + size + '" fill="#fff"/><path d="' + d + '" fill="#000"/></svg>';
      var svg = el.firstChild;
      svg.setAttribute('aria-label', el.getAttribute('aria-label') || 'QR');
    });
  }

  // ---------------------------------------------------------------- one payment for several guests
  // Adds up the ticked guests and puts the names and amount in the question
  // shown before the payment is saved.
  $$('form[data-together]').forEach(function (form) {
    var sumEl = $('[data-together-sum]', form);
    var picked = function () { return $$('input[name="guest"]:checked', form); };
    var total = function () { return picked().reduce(function (n, b) { return n + (+b.getAttribute('data-cents') || 0); }, 0); };
    var names = function () {
      var gs = picked().map(function (b) { return b.value; });
      return gs.length > 1 ? gs.slice(0, -1).join(', ') + ' & ' + gs[gs.length - 1] : gs.join('');
    };
    var update = function () {
      var n = picked().length;
      sumEl.textContent = n ? euro(total()) : '';
      $$('button[name="method"]', form).forEach(function (b) { b.disabled = n === 0 || b.hasAttribute('data-locked'); });
    };
    $$('button[name="method"]', form).forEach(function (b) {
      if (b.disabled) b.setAttribute('data-locked', '');
      b.addEventListener('click', function () {
        var tpl = form.getAttribute(b.value === 'cash' ? 'data-tpl-cash' : 'data-tpl-card') || '';
        form.setAttribute('data-confirm', tpl.replace('%s', names()).replace('%s', euro(total())));
      });
    });
    form.addEventListener('change', function (e) {
      var chip = e.target.closest('.chip-g');
      if (chip) chip.classList.toggle('on', e.target.checked);
      update();
    });
    update();
  });

  // ---------------------------------------------------------------- splitting a bill
  // Tapping a guest on a dish saves at once and updates every guest's total,
  // without reloading. (Without this, each dish has its own Save button.)
  var splitWho = $('#who');
  if (splitWho) {
    var swap = function () {
      return fetch(location.pathname + location.search, { credentials: 'same-origin' })
        .then(function (res) { return res.ok ? res.text() : null; })
        .then(function (html) {
          if (!html) return;
          var doc = new DOMParser().parseFromString(html, 'text/html');
          ['pay'].forEach(function (id) {
            var fresh = doc.getElementById(id), old = document.getElementById(id);
            if (fresh && old) old.replaceWith(fresh);
          });
          // the dishes still waiting for a guest
          $$('.sl', splitWho).forEach(function (li, i) {
            var f = doc.querySelectorAll('#who .sl')[i];
            if (f) li.classList.toggle('sl-open', f.classList.contains('sl-open'));
          });
          var note = $('.notice', splitWho), freshNote = doc.querySelector('#who .notice');
          if (note && !freshNote) note.remove();
          else if (note && freshNote) note.textContent = freshNote.textContent;
          else if (!note && freshNote) splitWho.appendChild(document.importNode(freshNote, true));
        });
    };
    splitWho.addEventListener('change', function (e) {
      var box = e.target;
      var form = box.closest('form[data-autosave]');
      if (form) {
        var chip = box.closest('.chip-g');
        if (chip) chip.classList.toggle('on', box.checked);
        fetch(form.action, {
          method: 'POST', credentials: 'same-origin', headers: { 'X-Live': '1' },
          body: new URLSearchParams(new FormData(form))
        }).then(function (res) {
          if (res.ok) return swap();
          location.reload(); // refused (a guest already paid): show how it really is
        }).catch(function () { location.reload(); });
        return;
      }
      if (box.hasAttribute('data-autosubmit-select')) {
        box.form.requestSubmit ? box.form.requestSubmit() : box.form.submit();
      }
    });
  }

  // Menu editor: the on/off switch saves straight away.
  // Staff date pickers jump to the chosen day straight away.
  $$('form[data-autosubmit] input[type="checkbox"], form[data-autosubmit] input[type="date"]').forEach(function (c) {
    c.addEventListener('change', function () { c.form.requestSubmit ? c.form.requestSubmit() : c.form.submit(); });
  });

  // Calendar link box: one tap selects the whole link for copying.
  $$('input[data-select]').forEach(function (i) {
    i.addEventListener('click', function () { i.select(); });
  });

  // ---------------------------------------------------------------- no internet
  // Staff screens stay open all evening. If the internet goes (or the server
  // can't be reached twice in a row), a red box tells staff to switch to
  // paper. Its words are already in the page, so they show without a
  // connection; it goes away by itself when the connection is back.
  var offBar = $('#offline-bar');
  if (offBar) {
    var offFails = 0;
    var setOff = function (down) {
      offBar.hidden = !down;
      document.body.classList.toggle('is-offline', down);
    };
    var ping = function () {
      if (document.hidden) return;
      var ctl = window.AbortController ? new AbortController() : null;
      var timer = setTimeout(function () { if (ctl) ctl.abort(); }, 8000);
      fetch('/healthz', { cache: 'no-store', credentials: 'same-origin', signal: ctl ? ctl.signal : undefined })
        .then(function (res) { if (!res.ok) throw new Error('down'); offFails = 0; setOff(false); })
        .catch(function () { offFails++; if (offFails >= 2 || navigator.onLine === false) setOff(true); })
        .then(function () { clearTimeout(timer); });
    };
    window.addEventListener('offline', function () { setOff(true); });
    window.addEventListener('online', function () { offFails = 0; ping(); });
    document.addEventListener('visibilitychange', function () { if (!document.hidden) ping(); });
    setInterval(ping, 20000);
    if (navigator.onLine === false) setOff(true);
  }

  // ---------------------------------------------------------------- menu search
  // "Sök produkt": type part of a name to see only those dishes and drinks.
  $$('[data-dish-search]').forEach(function (box) {
    var form = box.closest('[data-search-scope]') || box.closest('form');
    var input = $('input[type=search]', box) || $('input', box);
    var none = $('[data-search-none]', box);
    if (!form || !input) return;
    box.hidden = false;
    var norm = function (t) { return (t || '').toLowerCase().normalize('NFD').replace(/[\u0300-\u036f]/g, ''); };
    // Stock page: Food / Drinks / Both, remembered on this device.
    var kinds = $$('[data-kind-filter] input', box);
    var kindKey = 'ozmo-stock-kind';
    try { var saved = localStorage.getItem(kindKey); if (saved) kinds.forEach(function (k) { k.checked = k.value === saved; }); } catch (e) {}
    var kind = function () { var k = kinds.filter(function (x) { return x.checked; })[0]; return k ? k.value : 'all'; };
    var filter = function () {
      var q = norm(input.value.trim());
      var kd = kind();
      var narrowed = !!q || kd !== 'all';
      var shown = 0;
      $$('.dish.od', form).forEach(function (li) {
        var name = norm(($('.od-name', li) || li).textContent);
        var hit = (!q || name.indexOf(q) > -1) && (kd === 'all' || !li.dataset.kind || li.dataset.kind === kd);
        li.hidden = !hit;
        if (hit) shown++;
      });
      $$('.od-cat', form).forEach(function (h) {
        var list = h.nextElementSibling;
        var any = list && $$('.dish.od', list).some(function (li) { return !li.hidden; });
        h.hidden = narrowed && !any;
        if (list) list.hidden = narrowed && !any;
      });
      $$('.stock-chips [data-kind]', form).forEach(function (a) { a.hidden = kd !== 'all' && a.dataset.kind !== kd; });
      $$('[data-kind-section]', form).forEach(function (sec) { sec.hidden = kd !== 'all' && sec.dataset.kindSection !== kd; });
      var jump = $('.od-jump', form);
      if (jump) jump.hidden = !!q;
      if (none) none.hidden = shown > 0;
    };
    input.addEventListener('input', filter);
    kinds.forEach(function (k) { k.addEventListener('change', function () { try { localStorage.setItem(kindKey, kind()); } catch (e) {} filter(); }); });
    if (kinds.length) filter();
    document.addEventListener('live:redrawn', filter); // the list was redrawn: keep it filtered
    input.addEventListener('keydown', function (e) { if (e.key === 'Enter') e.preventDefault(); });
  });

  // ---------------------------------------------------------------- 3 meals: a saved order fills in the meals
  $$('[data-plan-pick]').forEach(function (sel) {
    var form = sel.closest('form');
    sel.addEventListener('change', function () {
      var opt = sel.options[sel.selectedIndex];
      var dishes = (opt && opt.getAttribute('data-dishes') || '').split('|');
      $$('input[name^="dish"]', form).forEach(function (inp, i) { inp.value = opt && opt.value ? (dishes[i] || '') : ''; });
    });
  });

  // ---------------------------------------------------------------- paying part of a bill
  // A gift card or lunch booklet needs its code; a booklet pays per coupon.
  $$('[data-pay-part]').forEach(function (form) {
    var sel = $('[data-pay-method]', form);
    var show = function () {
      var m = sel.value;
      $('[data-pay-code]', form).hidden = m !== 'giftcard' && m !== 'lunchcoupon';
      $('[data-pay-coupons]', form).hidden = m !== 'lunchcoupon';
      $('[data-pay-amount]', form).hidden = m === 'lunchcoupon';
      $('[data-pay-gift-hint]', form).hidden = m !== 'giftcard';
    };
    sel.addEventListener('change', show);
    show();
  });

  // ---------------------------------------------------------------- till keypad
  // Big number keys fill the waiter number, then the PIN (like the old till).
  $$('[data-till]').forEach(function (form) {
    var pad = $('[data-till-pad]', form);
    var fields = $$('[data-till-field]', form);
    if (!pad || fields.length < 2) return;
    pad.hidden = false;
    var current = fields[0];
    fields.forEach(function (f) { f.addEventListener('focus', function () { current = f; }); });
    pad.addEventListener('click', function (e) {
      var k = e.target.closest && e.target.closest('[data-key]');
      if (!k) return;
      var key = k.getAttribute('data-key');
      if (key === 'C') { fields.forEach(function (f) { f.value = ''; }); current = fields[0]; }
      else if (key === '⌫') { current.value = current.value.slice(0, -1); }
      else if (current.value.length < (+current.getAttribute('maxlength') || 6)) {
        current.value += key;
        // after the waiter number (1–2 digits), the next key goes to the PIN
        if (current === fields[0] && current.value.length >= 2) current = fields[1];
      }
      current.focus({ preventScroll: true });
    });
    fields[0].addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); fields[1].focus(); } });
  });

  // ---------------------------------------------------------------- room tabs
  // Orders and Tables show the tables room by room. A tab shows one room (or
  // all of them); the choice is remembered on this device for each screen.
  function applyRooms() {
    $$('[data-room-view]').forEach(function (view) {
      var show = view.getAttribute('data-show');
      if (!show) {
        try { show = localStorage.getItem('room:' + view.getAttribute('data-room-view')) || 'all'; } catch (e) { show = 'all'; }
      }
      var known = show === 'all';
      $$('[data-room]', view).forEach(function (sec) { if (sec.getAttribute('data-room') === show) known = true; });
      if (!known) show = 'all';
      view.setAttribute('data-show', show);
      $$('[data-room]', view).forEach(function (sec) { sec.hidden = show !== 'all' && sec.getAttribute('data-room') !== show; });
      $$('[data-room-tab]', view).forEach(function (b) {
        var on = b.getAttribute('data-room-tab') === show;
        b.setAttribute('aria-pressed', on ? 'true' : 'false');
      });
    });
  }
  if ($('[data-room-view]')) {
    document.addEventListener('click', function (e) {
      var b = e.target.closest && e.target.closest('[data-room-tab], [data-room-go]');
      var view = b && b.closest('[data-room-view]');
      if (!view) view = $('[data-room-view]');
      if (!b || !view) return;
      var show = b.getAttribute('data-room-tab') || b.getAttribute('data-room-go');
      if (b.hasAttribute('data-room-go')) {
        // A call for a table in a room that's hidden: show that room.
        var cur = view.getAttribute('data-show');
        if (cur === 'all' || cur === show) return;
      }
      view.setAttribute('data-show', show);
      try { localStorage.setItem('room:' + view.getAttribute('data-room-view'), show); } catch (e2) {}
      applyRooms();
    });
    document.addEventListener('live:redrawn', applyRooms);
    applyRooms();
  }
})();

/* Notifications on this phone (push): the bell button on Orders and the
   kitchen screen. Works when the page is closed and the phone is locked. */
(function () {
  var btn = document.querySelector('[data-push]');
  if (!btn) return;
  var ok = window.isSecureContext && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  if (!ok) {
    var hint = document.querySelector('[data-push-hint]');
    if (hint && /iPhone|iPad|iPod/.test(navigator.userAgent)) hint.hidden = false;
    return;
  }
  btn.hidden = false;
  var reg = null;
  function key(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) s += '=';
    var raw = atob(s), out = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out;
  }
  function show(on) {
    btn.textContent = on ? btn.getAttribute('data-text-on') : btn.getAttribute('data-text-off');
    btn.classList.toggle('on', on);
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
  }
  function post(url, sub) {
    return fetch(url, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(sub) });
  }
  navigator.serviceWorker.register('/sw.js', { scope: '/admin/' }).then(function (r) {
    reg = r;
    return r.pushManager.getSubscription();
  }).then(function (sub) {
    var on = !!sub && Notification.permission === 'granted';
    show(on);
    if (on) post('/admin/push/subscribe', sub); // keep the server's copy (and whose phone it is) fresh
  }).catch(function () { btn.hidden = true; });
  btn.addEventListener('click', function () {
    if (!reg) return;
    btn.disabled = true;
    reg.pushManager.getSubscription().then(function (sub) {
      if (sub) {
        return post('/admin/push/unsubscribe', sub).then(function () { return sub.unsubscribe(); }).then(function () { show(false); });
      }
      return Notification.requestPermission().then(function (p) {
        if (p !== 'granted') { btn.textContent = btn.getAttribute('data-text-denied'); return; }
        return fetch('/admin/push/key', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (k) {
          return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key(k.key) });
        }).then(function (s) {
          return post('/admin/push/subscribe', s).then(function (r) { if (!r.ok) throw new Error('save'); show(true); });
        });
      });
    }).catch(function () {
      btn.textContent = btn.getAttribute('data-text-fail');
    }).then(function () { btn.disabled = false; });
  });
})();

/* A dialog that opens by itself (the receipt question after paying), and a
   page that prints by itself (?print=1, after the QR code is drawn). */
(function () {
  var d = document.querySelector('dialog[data-autoshow]');
  if (d && d.showModal) {
    d.showModal();
    var p = d.getAttribute('data-drop-param');
    if (p && window.history && history.replaceState) { // a reload doesn't ask again
      var u = new URL(location.href);
      u.searchParams.delete(p);
      u.searchParams.delete('msg');
      history.replaceState(null, '', u.pathname + (u.search || '') + u.hash);
    }
  }
  if (document.querySelector('[data-autoprint]')) {
    window.addEventListener('load', function () { setTimeout(function () { window.print(); }, 350); });
  }
})();
