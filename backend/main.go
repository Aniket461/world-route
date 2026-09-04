package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type LatLng struct {
	Name string  `json:"name"`
	Lng  float64 `json:"lng"`
	Lat  float64 `json:"lat"`
}

type PlanRequest struct {
	Profile string   `json:"profile"`
	Start   LatLng   `json:"start"`
	End     LatLng   `json:"end"`
	Places  []LatLng `json:"places"`
}

type PlanResponse struct {
	Ordered    []LatLng       `json:"ordered"`
	Legs       []RouteLeg     `json:"legs"`
	DistanceM  float64        `json:"distanceM"`
	DurationS  float64        `json:"durationS"`
	Geometry   map[string]any `json:"geometry"`
	WaypointN  int            `json:"waypointCount"`
	Profile    string         `json:"profile"`
	Warning    string         `json:"warning,omitempty"`
}

type RouteLeg struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	DistanceM float64 `json:"distanceM"`
	DurationS float64 `json:"durationS"`
}

type SearchHit struct {
	ID         string   `json:"id,omitempty"`
	Name       string   `json:"name"`
	Address    string   `json:"address"`
	Type       string   `json:"type,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Maki       string   `json:"maki,omitempty"`
	Reviews    int      `json:"-"`
	Lng        float64  `json:"lng"`
	Lat        float64  `json:"lat"`
}

func main() {
	loadDotEnv()
	token := strings.TrimSpace(os.Getenv("MAPBOX_ACCESS_TOKEN"))
	if token == "" {
		log.Fatal("MAPBOX_ACCESS_TOKEN is missing. Copy .env.example to .env and add your public Mapbox token.")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	origins := parseCORSOrigins(os.Getenv("CORS_ORIGIN"))
	if len(origins) == 0 {
		origins = []string{"http://localhost:4200"}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"mapboxToken": token})
	})
	mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(q) < 2 {
			writeJSON(w, http.StatusOK, []SearchHit{})
			return
		}
		session := strings.TrimSpace(r.URL.Query().Get("session"))
		if session == "" {
			session = newSessionToken()
		}
		proximity := strings.TrimSpace(r.URL.Query().Get("proximity"))

		suggestHits, suggestErr := searchSuggest(token, q, session, proximity)
		forwardHits, _ := searchForward(token, q, proximity)
		for _, alt := range alternateQueries(q) {
			extra, _ := searchForward(token, alt, proximity)
			forwardHits = append(forwardHits, extra...)
		}
		hits := mergeSearchHits(q, suggestHits, forwardHits)
		if len(hits) == 0 && suggestErr != nil {
			writeErr(w, http.StatusBadGateway, suggestErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, hits)
	})
	mux.HandleFunc("GET /api/search/retrieve", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		session := strings.TrimSpace(r.URL.Query().Get("session"))
		if id == "" || session == "" {
			writeErr(w, http.StatusBadRequest, "id and session are required")
			return
		}
		hit, err := searchRetrieve(token, id, session)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, hit)
	})
	mux.HandleFunc("GET /api/reverse", func(w http.ResponseWriter, r *http.Request) {
		lng, err1 := strconv.ParseFloat(r.URL.Query().Get("lng"), 64)
		lat, err2 := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
		if err1 != nil || err2 != nil {
			writeErr(w, http.StatusBadRequest, "lng and lat are required")
			return
		}
		hit, err := reverseLookup(token, lng, lat)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, hit)
	})
	mux.HandleFunc("POST /api/plan", func(w http.ResponseWriter, r *http.Request) {
		var req PlanRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		resp, status, err := optimize(token, req)
		if err != nil {
			writeErr(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	handler := withCORS(origins, mux)
	addr := ":" + port
	// Use stdout so Railway does not classify the boot line as an error (Go log → stderr).
	fmt.Printf("world-route API listening on %s (CORS: %s)\n", addr, strings.Join(origins, ", "))
	log.Fatal(http.ListenAndServe(addr, handler))
}

func optimize(token string, req PlanRequest) (*PlanResponse, int, error) {
	if !validCoord(req.Start) || !validCoord(req.End) {
		return nil, http.StatusBadRequest, fmt.Errorf("start and end locations are required")
	}
	profile := req.Profile
	if profile == "" {
		profile = "mapbox/driving"
	}
	allowed := map[string]bool{
		"mapbox/driving": true, "mapbox/driving-traffic": true,
		"mapbox/walking": true, "mapbox/cycling": true,
	}
	if !allowed[profile] {
		return nil, http.StatusBadRequest, fmt.Errorf("unsupported profile")
	}

	points := []LatLng{req.Start}
	points = append(points, req.Places...)
	points = append(points, req.End)
	if len(points) < 2 {
		return nil, http.StatusBadRequest, fmt.Errorf("need at least start and end")
	}
	if len(points) > 12 {
		return nil, http.StatusBadRequest, fmt.Errorf("Mapbox Optimization v1 allows at most 12 points (start, end, and up to 10 stops)")
	}

	coords := make([]string, len(points))
	for i, p := range points {
		coords[i] = fmt.Sprintf("%f,%f", p.Lng, p.Lat)
	}
	// Profile must keep its slash (mapbox/driving). Do not PathEscape the whole profile.
	endpoint := fmt.Sprintf(
		"https://api.mapbox.com/optimized-trips/v1/%s/%s",
		profile,
		strings.Join(coords, ";"),
	)
	q := url.Values{}
	q.Set("access_token", token)

	samePlace := approxEqual(req.Start.Lng, req.End.Lng) && approxEqual(req.Start.Lat, req.End.Lat)
	if samePlace {
		// One-way optimize rejects identical start/end; treat as a round trip back to start.
		points = points[:len(points)-1]
		coords = coords[:len(coords)-1]
		endpoint = fmt.Sprintf(
			"https://api.mapbox.com/optimized-trips/v1/%s/%s",
			profile,
			strings.Join(coords, ";"),
		)
		q.Set("roundtrip", "true")
		q.Set("source", "first")
		q.Set("destination", "any")
	} else {
		q.Set("roundtrip", "false")
		q.Set("source", "first")
		q.Set("destination", "last")
	}
	q.Set("geometries", "geojson")
	q.Set("overview", "full")
	q.Set("steps", "false")

	body, status, err := mapboxGET(endpoint + "?" + q.Encode())
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if status >= 400 {
		return nil, http.StatusBadGateway, fmt.Errorf("mapbox optimization failed: %s", strings.TrimSpace(string(body)))
	}

	var raw struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Waypoints []struct {
			Name          string    `json:"name"`
			Location      []float64 `json:"location"`
			WaypointIndex int       `json:"waypoint_index"`
		} `json:"waypoints"`
		Trips []struct {
			Distance float64        `json:"distance"`
			Duration float64        `json:"duration"`
			Geometry map[string]any `json:"geometry"`
			Legs     []struct {
				Distance float64 `json:"distance"`
				Duration float64 `json:"duration"`
			} `json:"legs"`
		} `json:"trips"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("could not parse mapbox response")
	}
	if raw.Code != "Ok" || len(raw.Trips) == 0 {
		msg := raw.Code
		if raw.Message != "" {
			msg = raw.Message
		}
		if msg == "" {
			msg = "no optimized trip found"
		}
		return nil, http.StatusUnprocessableEntity, fmt.Errorf("%s — Mapbox could not route those points on the road network (oceans or disconnected regions often fail)", msg)
	}

	ordered := make([]LatLng, len(points))
	for i, wp := range raw.Waypoints {
		if wp.WaypointIndex < 0 || wp.WaypointIndex >= len(ordered) {
			continue
		}
		label := points[i].Name
		if label == "" && wp.Name != "" {
			label = wp.Name
		}
		lng, lat := points[i].Lng, points[i].Lat
		if len(wp.Location) == 2 {
			lng, lat = wp.Location[0], wp.Location[1]
		}
		ordered[wp.WaypointIndex] = LatLng{Name: label, Lng: lng, Lat: lat}
	}

	warning := ""
	if samePlace {
		warning = "Start and end are the same place, so this was optimized as a round trip returning to the start."
		ordered = append(ordered, req.Start)
	}
	if len(points) >= 10 {
		if warning != "" {
			warning += " "
		}
		warning += "With 10 or more coordinates, Mapbox returns an optimized approximation rather than a guaranteed optimum."
	}

	trip := raw.Trips[0]
	legs := make([]RouteLeg, 0, len(trip.Legs))
	for i, leg := range trip.Legs {
		fromName, toName := "", ""
		if i < len(ordered) {
			fromName = ordered[i].Name
		}
		if i+1 < len(ordered) {
			toName = ordered[i+1].Name
		}
		legs = append(legs, RouteLeg{
			From:      fromName,
			To:        toName,
			DistanceM: leg.Distance,
			DurationS: leg.Duration,
		})
	}

	return &PlanResponse{
		Ordered:   ordered,
		Legs:      legs,
		DistanceM: trip.Distance,
		DurationS: trip.Duration,
		Geometry:  trip.Geometry,
		WaypointN: len(points),
		Profile:   profile,
		Warning:   warning,
	}, http.StatusOK, nil
}

func searchSuggest(token, query, session, proximity string) ([]SearchHit, error) {
	vals := url.Values{
		"q":            {query},
		"language":     {"en"},
		"limit":        {"8"},
		"types":        {"poi,address,place,street,locality,neighborhood,region,country"},
		"session_token": {session},
		"access_token": {token},
	}
	if proximity != "" {
		vals.Set("proximity", proximity)
	}
	body, status, err := mapboxGET("https://api.mapbox.com/search/searchbox/v1/suggest?" + vals.Encode())
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("search suggest failed: %s", strings.TrimSpace(string(body)))
	}
	var raw struct {
		Suggestions []struct {
			Name          string `json:"name"`
			NamePreferred string `json:"name_preferred"`
			MapboxID      string `json:"mapbox_id"`
			FeatureType   string `json:"feature_type"`
			FullAddress   string `json:"full_address"`
			PlaceFormatted string `json:"place_formatted"`
			Address       string `json:"address"`
		} `json:"suggestions"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	hits := make([]SearchHit, 0, len(raw.Suggestions))
	for _, s := range raw.Suggestions {
		if s.FeatureType == "category" {
			continue
		}
		name := s.NamePreferred
		if name == "" {
			name = s.Name
		}
		addr := s.FullAddress
		if addr == "" {
			addr = s.PlaceFormatted
		}
		if addr == "" {
			addr = s.Address
		}
		hits = append(hits, SearchHit{
			ID:      s.MapboxID,
			Name:    name,
			Address: addr,
			Type:    s.FeatureType,
		})
	}
	return hits, nil
}

func searchRetrieve(token, id, session string) (SearchHit, error) {
	vals := url.Values{
		"session_token": {session},
		"language":      {"en"},
		"access_token":  {token},
	}
	endpoint := "https://api.mapbox.com/search/searchbox/v1/retrieve/" + url.PathEscape(id) + "?" + vals.Encode()
	body, status, err := mapboxGET(endpoint)
	if err != nil {
		return SearchHit{}, err
	}
	if status >= 400 {
		return SearchHit{}, fmt.Errorf("search retrieve failed: %s", strings.TrimSpace(string(body)))
	}
	hits, err := parseSearchFeatures(body)
	if err != nil {
		return SearchHit{}, err
	}
	if len(hits) == 0 {
		return SearchHit{}, fmt.Errorf("no details for selected place")
	}
	return hits[0], nil
}

func searchForward(token, query, proximity string) ([]SearchHit, error) {
	vals := url.Values{
		"q":            {query},
		"language":     {"en"},
		"limit":        {"8"},
		"types":        {"poi,address,place,street,locality,neighborhood"},
		"access_token": {token},
	}
	if proximity != "" {
		vals.Set("proximity", proximity)
	}
	body, status, err := mapboxGET("https://api.mapbox.com/search/searchbox/v1/forward?" + vals.Encode())
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		// Last resort: classic geocoder.
		return geocodeForward(token, query, proximity)
	}
	hits, err := parseSearchFeatures(body)
	if err != nil || len(hits) == 0 {
		return geocodeForward(token, query, proximity)
	}
	return hits, nil
}

func geocodeForward(token, query, proximity string) ([]SearchHit, error) {
	vals := url.Values{
		"q":            {query},
		"limit":        {"8"},
		"language":     {"en"},
		"autocomplete": {"true"},
		"types":        {"poi,address,place,locality,neighborhood,street"},
		"access_token": {token},
	}
	if proximity != "" {
		vals.Set("proximity", proximity)
	}
	body, status, err := mapboxGET("https://api.mapbox.com/search/geocode/v6/forward?" + vals.Encode())
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("geocoding failed: %s", strings.TrimSpace(string(body)))
	}
	return parseGeocodeFeatures(body)
}

func reverseLookup(token string, lng, lat float64) (SearchHit, error) {
	// Prefer Search Box reverse so landmarks/POIs (Eiffel Tower, etc.) come back by name.
	vals := url.Values{
		"longitude":    {strconv.FormatFloat(lng, 'f', 6, 64)},
		"latitude":     {strconv.FormatFloat(lat, 'f', 6, 64)},
		"language":     {"en"},
		"limit":        {"8"},
		"types":        {"poi,address,street,place,locality,neighborhood"},
		"access_token": {token},
	}
	body, status, err := mapboxGET("https://api.mapbox.com/search/searchbox/v1/reverse?" + vals.Encode())
	if err == nil && status < 400 {
		hits, perr := parseSearchFeatures(body)
		if perr == nil && len(hits) > 0 {
			if pick := preferLandmark(hits, lng, lat); pick.Name != "" {
				// Keep the dropped pin exact if POI is nearby but coords differ slightly.
				if pick.Type == "poi" {
					pick.Lng = lng
					pick.Lat = lat
				}
				return pick, nil
			}
		}
	}

	// Fallback: classic reverse geocoder (addresses only).
	return geocodeReverse(token, lng, lat)
}

// preferLandmark picks a nearby landmark/POI when the user clicked one; otherwise the best address/place.
func preferLandmark(hits []SearchHit, lng, lat float64) SearchHit {
	const maxPOIMeters = 180.0
	var bestPOI SearchHit
	bestScore := -1_000_000
	var fallback SearchHit

	for _, hit := range hits {
		if fallback.Name == "" && hit.Type != "poi" {
			fallback = hit
		}
		if hit.Type != "poi" {
			continue
		}
		d := haversineMeters(lat, lng, hit.Lat, hit.Lng)
		if d > maxPOIMeters {
			continue
		}
		score := landmarkScore(hit) - int(d) // closer wins ties
		if score > bestScore {
			bestPOI = hit
			bestScore = score
		}
	}

	if bestPOI.Name != "" && bestScore > 0 {
		if bestPOI.Address == "" && fallback.Address != "" {
			bestPOI.Address = fallback.Address
		} else if bestPOI.Address == "" && fallback.Name != "" {
			bestPOI.Address = fallback.Name
		}
		return bestPOI
	}
	if fallback.Name != "" {
		return fallback
	}
	return hits[0]
}

func landmarkScore(hit SearchHit) int {
	score := 10
	cats := append([]string{}, hit.Categories...)
	cats = append(cats, hit.Maki)
	joined := strings.ToLower(strings.Join(cats, " "))

	landmarkHints := []string{
		"tourist_attraction", "tourist attraction", "attraction", "viewpoint", "monument",
		"museum", "historic", "castle", "landmark", "park", "outdoors", "stadium",
		"place_of_worship", "church", "mosque", "temple", "gallery", "zoo", "aquarium",
		"airport", "railway_station", "rail", "station", "ferry",
	}
	noiseHints := []string{
		"bar", "restaurant", "food", "nightlife", "cafe", "coffee", "shop", "store",
		"psychic", "services", "gym", "fitness", "taxi", "bathroom", "toilet", "parking",
		"hotel", "hostel", "apartment",
	}
	for _, h := range landmarkHints {
		if strings.Contains(joined, h) {
			score += 120
			break
		}
	}
	for _, h := range noiseHints {
		if strings.Contains(joined, h) {
			score -= 80
			break
		}
	}
	// Well-known places often have many reviews.
	if hit.Reviews > 1000 {
		score += 60
	} else if hit.Reviews > 100 {
		score += 25
	}
	name := strings.ToLower(hit.Name)
	for _, h := range []string{"tower", "cathedral", "basilica", "palace", "colosseum", "museum", "castle", "bridge", "park", "square", "plaza", "station", "airport"} {
		if strings.Contains(name, h) {
			score += 40
			break
		}
	}
	return score
}

func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371000.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	φ1, φ2 := toRad(lat1), toRad(lat2)
	Δφ := toRad(lat2 - lat1)
	Δλ := toRad(lng2 - lng1)
	a := math.Sin(Δφ/2)*math.Sin(Δφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(Δλ/2)*math.Sin(Δλ/2)
	return 2 * r * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func geocodeReverse(token string, lng, lat float64) (SearchHit, error) {
	u := "https://api.mapbox.com/search/geocode/v6/reverse?" + url.Values{
		"longitude":    {strconv.FormatFloat(lng, 'f', 6, 64)},
		"latitude":     {strconv.FormatFloat(lat, 'f', 6, 64)},
		"limit":        {"1"},
		"types":        {"address,street,place,locality,neighborhood,district,region,country"},
		"access_token": {token},
	}.Encode()
	body, status, err := mapboxGET(u)
	if err != nil {
		return SearchHit{}, err
	}
	if status >= 400 {
		return SearchHit{}, fmt.Errorf("reverse geocoding failed: %s", strings.TrimSpace(string(body)))
	}
	hits, err := parseGeocodeFeatures(body)
	if err != nil {
		return SearchHit{}, err
	}
	if len(hits) == 0 {
		return SearchHit{
			Name:    fmt.Sprintf("%.4f, %.4f", lat, lng),
			Address: "Dropped pin",
			Type:    "pin",
			Lng:     lng,
			Lat:     lat,
		}, nil
	}
	return hits[0], nil
}

func parseSearchFeatures(body []byte) ([]SearchHit, error) {
	var raw struct {
		Features []struct {
			Properties struct {
				Name           string   `json:"name"`
				NamePreferred  string   `json:"name_preferred"`
				FullAddress    string   `json:"full_address"`
				PlaceFormatted string   `json:"place_formatted"`
				FeatureType    string   `json:"feature_type"`
				MapboxID       string   `json:"mapbox_id"`
				Maki           string   `json:"maki"`
				POICategory    []string `json:"poi_category"`
				POICategoryIDs []string `json:"poi_category_ids"`
				Metadata       struct {
					ReviewCount int `json:"review_count"`
				} `json:"metadata"`
				Coordinates struct {
					Longitude float64 `json:"longitude"`
					Latitude  float64 `json:"latitude"`
				} `json:"coordinates"`
			} `json:"properties"`
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	hits := make([]SearchHit, 0, len(raw.Features))
	for _, f := range raw.Features {
		lng, lat := 0.0, 0.0
		if len(f.Geometry.Coordinates) >= 2 {
			lng, lat = f.Geometry.Coordinates[0], f.Geometry.Coordinates[1]
		} else {
			lng, lat = f.Properties.Coordinates.Longitude, f.Properties.Coordinates.Latitude
		}
		name := f.Properties.NamePreferred
		if name == "" {
			name = f.Properties.Name
		}
		addr := f.Properties.FullAddress
		if addr == "" {
			addr = f.Properties.PlaceFormatted
		}
		if name == "" {
			name = addr
		}
		cats := append([]string{}, f.Properties.POICategoryIDs...)
		cats = append(cats, f.Properties.POICategory...)
		hits = append(hits, SearchHit{
			ID:         f.Properties.MapboxID,
			Name:       name,
			Address:    addr,
			Type:       f.Properties.FeatureType,
			Categories: cats,
			Maki:       f.Properties.Maki,
			Reviews:    f.Properties.Metadata.ReviewCount,
			Lng:        lng,
			Lat:        lat,
		})
	}
	return hits, nil
}

func parseGeocodeFeatures(body []byte) ([]SearchHit, error) {
	var raw struct {
		Features []struct {
			Properties struct {
				Name           string `json:"name"`
				FullAddress    string `json:"full_address"`
				PlaceFormatted string `json:"place_formatted"`
				FeatureType    string `json:"feature_type"`
				Coordinates    struct {
					Longitude float64 `json:"longitude"`
					Latitude  float64 `json:"latitude"`
				} `json:"coordinates"`
			} `json:"properties"`
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	hits := make([]SearchHit, 0, len(raw.Features))
	for _, f := range raw.Features {
		lng, lat := 0.0, 0.0
		if len(f.Geometry.Coordinates) >= 2 {
			lng, lat = f.Geometry.Coordinates[0], f.Geometry.Coordinates[1]
		} else {
			lng, lat = f.Properties.Coordinates.Longitude, f.Properties.Coordinates.Latitude
		}
		name := f.Properties.Name
		addr := f.Properties.FullAddress
		if addr == "" {
			addr = f.Properties.PlaceFormatted
		}
		if name == "" {
			name = addr
		}
		hits = append(hits, SearchHit{
			Name:    name,
			Address: addr,
			Type:    f.Properties.FeatureType,
			Lng:     lng,
			Lat:     lat,
		})
	}
	return hits, nil
}

func alternateQueries(query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	var alts []string
	replacements := []struct{ from, to string }{
		{"central station", "termini"},
		{"central station", "stazione centrale"},
		{"train station", "stazione"},
		{"railway station", "stazione"},
	}
	for _, r := range replacements {
		if strings.Contains(q, r.from) {
			alts = append(alts, strings.ReplaceAll(q, r.from, r.to))
		}
	}
	return alts
}

func mergeSearchHits(query string, batches ...[]SearchHit) []SearchHit {
	seen := map[string]bool{}
	merged := make([]SearchHit, 0, 12)
	for _, batch := range batches {
		for _, hit := range batch {
			key := strings.ToLower(strings.TrimSpace(hit.ID))
			if key == "" {
				key = strings.ToLower(hit.Name + "|" + hit.Address)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, hit)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return searchScore(query, merged[i]) > searchScore(query, merged[j])
	})
	if len(merged) > 8 {
		merged = merged[:8]
	}
	return merged
}

func searchScore(query string, hit SearchHit) int {
	q := strings.ToLower(query)
	name := strings.ToLower(hit.Name)
	addr := strings.ToLower(hit.Address)
	score := 0
	if strings.Contains(name, q) {
		score += 40
	}
	for _, part := range strings.Fields(q) {
		if len(part) < 3 {
			continue
		}
		if strings.Contains(name, part) {
			score += 12
		}
		if strings.Contains(addr, part) {
			score += 4
		}
	}
	transitWords := []string{"station", "stazione", "termini", "terminal", "airport", "aeroporto", "rail", "train", "metro", "bus"}
	queryWantsTransit := false
	for _, w := range transitWords {
		if strings.Contains(q, w) {
			queryWantsTransit = true
			break
		}
	}
	if queryWantsTransit {
		for _, w := range []string{"termini", "stazione centrale", "stazione", "railway", "train station"} {
			if strings.Contains(name, w) {
				score += 45
				break
			}
		}
		for _, w := range transitWords {
			if strings.Contains(name, w) || strings.Contains(addr, w) {
				score += 15
				break
			}
		}
		// Hotel/guest-house noise for transit queries.
		lodging := []string{"hotel", "guest house", "guesthouse", "hostel", "apartment", "suites", "room", "bnb", "b&b"}
		for _, w := range lodging {
			if strings.Contains(name, w) {
				score -= 55
				break
			}
		}
	}
	switch hit.Type {
	case "poi":
		score += 8
	case "address", "street":
		score += 5
	case "place", "locality":
		score += 3
	}
	if hit.Lng != 0 || hit.Lat != 0 {
		score += 2
	}
	return score
}

func newSessionToken() string {
	b := make([]byte, 16)
	_, _ = io.ReadFull(rand.Reader, b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func mapboxGET(rawURL string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "world-route/1.0")
	client := &http.Client{Timeout: 25 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, res.StatusCode, err
	}
	return body, res.StatusCode, nil
}

func validCoord(p LatLng) bool {
	if p.Lat < -90 || p.Lat > 90 || p.Lng < -180 || p.Lng > 180 {
		return false
	}
	return p.Lat != 0 || p.Lng != 0
}

func approxEqual(a, b float64) bool {
	const eps = 1e-5
	if a > b {
		return a-b < eps
	}
	return b-a < eps
}

func parseCORSOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		o := strings.TrimSpace(p)
		if o == "" || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	return out
}

func withCORS(allowed []string, next http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, o := range allowed {
		allow[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allow[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		} else if origin == "" && len(allowed) > 0 {
			// Non-browser clients (curl/health checks).
			w.Header().Set("Access-Control-Allow-Origin", allowed[0])
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func loadDotEnv() {
	candidates := []string{".env", filepath.Join("..", ".env")}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"), filepath.Join(filepath.Dir(exe), "..", ".env"))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			val = strings.TrimSpace(val)
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, val)
			}
		}
		return
	}
}
