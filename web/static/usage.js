// Customer dashboard scripts. Loaded without defer, so window.gbCountdown and window.portalLive exist before
// Alpine (deferred) starts. The live card polls /portal/usage.json every 15 s while the tab is visible; it
// shows the same speed meter as the admin online sessions page (sessions.js).
(function () {
  var EVERY_MS = 15000;

  // gbCountdown: the plan's time left, ticking once a second. Same wording as Go's pWords.countdown.
  window.gbCountdown = function (end, now, w) {
    var t0 = Date.now() / 1000;
    return {
      tick: 0,
      init: function () { var self = this; setInterval(function () { self.tick++; }, 1000); },
      get left() {
        this.tick;
        var s = Math.floor(end - (now + Date.now() / 1000 - t0));
        if (s <= 0) return w.done;
        var d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60), t;
        if (d > 0) t = d + " " + w.days + (h > 0 ? " " + h + " " + w.hours : "");
        else if (h > 0) t = h + " " + w.hours + (m > 0 ? " " + m + " " + w.minutes : "");
        else t = (m === 0 ? 1 : m) + " " + w.minutes;
        return t + " " + w.left;
      }
    };
  };

  window.portalLive = function () {
    return {
      live: {},
      status: '',
      url: '',
      labels: {},

      init: function () {
        var el = this.$el, st = {};
        try { st = JSON.parse(el.dataset.state || '{}'); } catch (e) { st = {}; }
        this.live = st;
        this.url = el.dataset.url;
        this.status = el.dataset.idle || '';
        this.labels = { updated: el.dataset.updated || '', failed: el.dataset.failed || '' };
        var self = this;
        setInterval(function () { if (!document.hidden) self.refresh(); }, EVERY_MS);
        document.addEventListener('visibilitychange', function () { if (!document.hidden) self.refresh(); });
      },

      refresh: function () {
        var self = this;
        return fetch(this.url, { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json' } })
          .then(function (res) {
            if (!res.ok) throw new Error('HTTP ' + res.status);
            return res.json();
          })
          .then(function (v) {
            self.live = v;
            self.status = self.labels.updated.replace('%s', new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }));
          })
          .catch(function () { self.status = self.labels.failed; });
      },

      // gauge is the dash length of the speedometer arc (pathLength 100).
      gauge: function () {
        return (this.live.online ? this.live.pct || 0 : 0) + ' 100';
      },

      // seg is the class of meter segment i: lit in the tone's colour, or the line colour.
      seg: function (i) {
        return 'h-3 w-1.5 rounded-sm ' + ((this.live.bars || [])[i] || 'bg-line');
      }
    };
  };
})();
