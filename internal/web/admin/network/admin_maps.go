package network

// Map pages and marker data for customers, routers and ODPs.

import (
	"encoding/json"
	"github.com/frand-kod/gobill/internal/web/core"
	"net/http"

	"strconv"
	"strings"
)

// parseCoords reads "lat,lng". ok is false unless both are numbers in range.
func parseCoords(s string) (lat, lng float64, ok bool) {
	a, b, found := strings.Cut(s, ",")
	if !found {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lng, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	ok = err1 == nil && err2 == nil && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
	return lat, lng, ok
}

// checkCoords validates an optional "lat,lng" and returns it normalized; "" stays "".
func checkCoords(s string) (string, bool) {
	if s == "" {
		return "", true
	}
	lat, lng, ok := parseCoords(s)
	if !ok {
		return s, false
	}
	return strconv.FormatFloat(lat, 'f', -1, 64) + "," + strconv.FormatFloat(lng, 'f', -1, 64), true
}

// MapMarker is one pin on a map: popup lines, and an optional coverage circle in meters.
type MapMarker struct {
	Lat    float64  `json:"lat"`
	Lng    float64  `json:"lng"`
	Title  string   `json:"title"`
	Lines  []string `json:"lines"`
	Radius int64    `json:"radius,omitempty"`
}

func writeMarkers(w http.ResponseWriter, ms []MapMarker) {
	if ms == nil {
		ms = []MapMarker{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"markers": ms})
}

// mapPage renders the Leaflet page; the marker JSON is fetched from dataURL.
func (s *Handlers) mapPage(title, dataURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.Render(w, r, http.StatusOK, "maps", core.Page{Title: title, Data: dataURL})
	}
}

// ponytail: loads every customer marker in one response and the map draws them all. Upgrade path when
// the operator has thousands of customers: a bbox query on the map viewport, or client-side clustering.
func (s *Handlers) mapCustomerData(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Queries.ListCustomerMarkers(r.Context())
	if err != nil {
		s.Fail(w, "customer markers", err)
		return
	}
	var ms []MapMarker
	for _, c := range rows {
		lat, lng, ok := parseCoords(c.Coordinates)
		if !ok {
			continue
		}
		plan := c.PlanName
		if plan == "" {
			plan = "-"
		}
		ms = append(ms, MapMarker{Lat: lat, Lng: lng, Title: c.Fullname, Lines: []string{"Plan: " + plan, "Status: " + c.Status}})
	}
	writeMarkers(w, ms)
}

func (s *Handlers) mapRouterData(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Queries.ListRouterMarkers(r.Context())
	if err != nil {
		s.Fail(w, "router markers", err)
		return
	}
	var ms []MapMarker
	for _, x := range rows {
		lat, lng, ok := parseCoords(x.Coordinates)
		if !ok {
			continue
		}
		state := "Disabled"
		if x.Enabled == 1 {
			state = "Enabled"
		}
		ms = append(ms, MapMarker{Lat: lat, Lng: lng, Title: x.Name, Radius: x.Coverage,
			Lines: []string{"Host: " + x.Host, "Status: " + state, "Coverage: " + strconv.FormatInt(x.Coverage, 10) + " m", x.Description}})
	}
	writeMarkers(w, ms)
}

func (s *Handlers) mapODPData(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Queries.ListODPMarkers(r.Context())
	if err != nil {
		s.Fail(w, "odp markers", err)
		return
	}
	var ms []MapMarker
	for _, o := range rows {
		lat, lng, ok := parseCoords(o.Coordinates)
		if !ok {
			continue
		}
		ms = append(ms, MapMarker{Lat: lat, Lng: lng, Title: o.Name, Radius: o.Coverage,
			Lines: []string{"Address: " + o.Address, "Ports: " + strconv.FormatInt(o.PortAmount, 10), "Attenuation: " + o.Attenuation, "Coverage: " + strconv.FormatInt(o.Coverage, 10) + " m"}})
	}
	writeMarkers(w, ms)
}
