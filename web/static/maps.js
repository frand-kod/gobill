// Leaflet helpers. Map pages: #map[data-src] loads markers as JSON. Forms: [data-pick] names an
// input that holds "lat,lng"; clicking the map (or dragging the pin) fills it in.
(function () {
  var TILES = 'https://tile.openstreetmap.org/{z}/{x}/{y}.png';
  var ATTR = '&copy; OpenStreetMap contributors';

  function base(el, zoom, center) {
    var m = L.map(el).setView(center, zoom);
    L.tileLayer(TILES, {maxZoom: 19, attribution: ATTR}).addTo(m);
    return m;
  }

  function mapPage(el) {
    var m = base(el, 5, [-2.5, 118]);
    var group = L.featureGroup().addTo(m);
    fetch(el.dataset.src, {credentials: 'same-origin'}).then(function (r) { return r.json(); }).then(function (data) {
      data.markers.forEach(function (k) {
        var pop = document.createElement('div');
        var title = document.createElement('strong');
        title.textContent = k.title;
        pop.appendChild(title);
        k.lines.forEach(function (line) {
          if (!line) return;
          var p = document.createElement('div');
          p.textContent = line;
          pop.appendChild(p);
        });
        L.marker([k.lat, k.lng]).bindPopup(pop).addTo(group);
        if (k.radius > 0) {
          L.circle([k.lat, k.lng], {radius: k.radius, color: '#2563eb', weight: 1, fillOpacity: 0.1}).addTo(group);
        }
      });
      if (data.markers.length) m.fitBounds(group.getBounds().pad(0.1));
    });
  }

  function picker(el) {
    var input = document.getElementById(el.dataset.pick);
    var p = input.value.split(',');
    var has = p.length === 2 && p[0].trim() !== '' && p[1].trim() !== '' && !isNaN(p[0]) && !isNaN(p[1]);
    var m = base(el, has ? 15 : 5, has ? [+p[0], +p[1]] : [-6.2, 106.8]);
    var pin = null;
    function set(ll) {
      if (pin) {
        pin.setLatLng(ll);
      } else {
        pin = L.marker(ll, {draggable: true}).addTo(m).on('dragend', function () { set(pin.getLatLng()); });
      }
      input.value = ll.lat.toFixed(6) + ',' + ll.lng.toFixed(6);
    }
    if (has) set(L.latLng(+p[0], +p[1]));
    m.on('click', function (e) { set(e.latlng); });
    input.addEventListener('change', function () {
      var q = input.value.split(',');
      if (q.length === 2 && !isNaN(q[0]) && !isNaN(q[1]) && q[0].trim() !== '' && q[1].trim() !== '') {
        set(L.latLng(+q[0], +q[1]));
        m.panTo(pin.getLatLng());
      }
    });
  }

  document.querySelectorAll('#map[data-src]').forEach(mapPage);
  document.querySelectorAll('[data-pick]').forEach(picker);
})();
