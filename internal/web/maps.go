package web

import (
	"encoding/json"
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

// mapMarker is one pin on a map: popup lines, and an optional coverage circle in meters.
type mapMarker struct {
	Lat    float64  `json:"lat"`
	Lng    float64  `json:"lng"`
	Title  string   `json:"title"`
	Lines  []string `json:"lines"`
	Radius int64    `json:"radius,omitempty"`
}

func writeMarkers(w http.ResponseWriter, ms []mapMarker) {
	if ms == nil {
		ms = []mapMarker{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"markers": ms})
}

// mapPage renders the Leaflet page; the marker JSON is fetched from dataURL.
func (s *Server) mapPage(title, dataURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, http.StatusOK, "maps", Page{Title: title, Data: dataURL})
	}
}

func (s *Server) mapCustomerData(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListCustomerMarkers(r.Context())
	if err != nil {
		s.fail(w, "customer markers", err)
		return
	}
	var ms []mapMarker
	for _, c := range rows {
		lat, lng, ok := parseCoords(c.Coordinates)
		if !ok {
			continue
		}
		plan := c.PlanName
		if plan == "" {
			plan = "-"
		}
		ms = append(ms, mapMarker{Lat: lat, Lng: lng, Title: c.Fullname, Lines: []string{"Plan: " + plan, "Status: " + c.Status}})
	}
	writeMarkers(w, ms)
}

func (s *Server) mapRouterData(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListRouterMarkers(r.Context())
	if err != nil {
		s.fail(w, "router markers", err)
		return
	}
	var ms []mapMarker
	for _, x := range rows {
		lat, lng, ok := parseCoords(x.Coordinates)
		if !ok {
			continue
		}
		state := "Disabled"
		if x.Enabled == 1 {
			state = "Enabled"
		}
		ms = append(ms, mapMarker{Lat: lat, Lng: lng, Title: x.Name, Radius: x.Coverage,
			Lines: []string{"Host: " + x.Host, "Status: " + state, "Coverage: " + strconv.FormatInt(x.Coverage, 10) + " m", x.Description}})
	}
	writeMarkers(w, ms)
}

func (s *Server) mapODPData(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListODPMarkers(r.Context())
	if err != nil {
		s.fail(w, "odp markers", err)
		return
	}
	var ms []mapMarker
	for _, o := range rows {
		lat, lng, ok := parseCoords(o.Coordinates)
		if !ok {
			continue
		}
		ms = append(ms, mapMarker{Lat: lat, Lng: lng, Title: o.Name, Radius: o.Coverage,
			Lines: []string{"Address: " + o.Address, "Ports: " + strconv.FormatInt(o.PortAmount, 10), "Attenuation: " + o.Attenuation, "Coverage: " + strconv.FormatInt(o.Coverage, 10) + " m"}})
	}
	writeMarkers(w, ms)
}
