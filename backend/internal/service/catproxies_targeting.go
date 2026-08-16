package service

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed data/catproxies_targeting.json
var catProxiesTargetingJSON []byte

type CatProxiesTargetOption struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type catProxiesTargetingSnapshot struct {
	Countries     []CatProxiesTargetOption `json:"countries"`
	USStates      []string                 `json:"us_states"`
	Cities        map[string][]string      `json:"cities"`
	USStateCities map[string][]string      `json:"us_state_cities"`
}

type CatProxiesTargetingResponse struct {
	Countries []CatProxiesTargetOption `json:"countries"`
	USStates  []string                 `json:"us_states"`
	Cities    []string                 `json:"cities"`
}

type CatProxiesTargetingService struct {
	snapshot  catProxiesTargetingSnapshot
	countries map[string]struct{}
	states    map[string]struct{}
}

func NewCatProxiesTargetingService() (*CatProxiesTargetingService, error) {
	var snapshot catProxiesTargetingSnapshot
	if err := json.Unmarshal(catProxiesTargetingJSON, &snapshot); err != nil {
		return nil, fmt.Errorf("parse embedded CatProxies targeting snapshot: %w", err)
	}
	if len(snapshot.Countries) == 0 || len(snapshot.USStates) == 0 || len(snapshot.Cities) == 0 {
		return nil, fmt.Errorf("embedded CatProxies targeting snapshot is incomplete")
	}
	svc := &CatProxiesTargetingService{
		snapshot:  snapshot,
		countries: make(map[string]struct{}, len(snapshot.Countries)),
		states:    make(map[string]struct{}, len(snapshot.USStates)),
	}
	for _, country := range snapshot.Countries {
		svc.countries[normalizeCatProxiesTargetToken(country.Code)] = struct{}{}
	}
	for _, state := range snapshot.USStates {
		svc.states[normalizeCatProxiesTargetToken(state)] = struct{}{}
	}
	return svc, nil
}

func (s *CatProxiesTargetingService) Get(country, state string) CatProxiesTargetingResponse {
	response := CatProxiesTargetingResponse{
		Countries: append([]CatProxiesTargetOption(nil), s.snapshot.Countries...),
		USStates:  append([]string(nil), s.snapshot.USStates...),
	}
	country = normalizeCatProxiesTargetToken(country)
	state = normalizeCatProxiesTargetToken(state)
	if country == "us" && state != "" {
		response.Cities = append([]string(nil), s.snapshot.USStateCities[state]...)
	} else if country != "" {
		response.Cities = append([]string(nil), s.snapshot.Cities[country]...)
	}
	if response.Cities == nil {
		response.Cities = []string{}
	}
	return response
}

func (s *CatProxiesTargetingService) ValidateTarget(country, state, city *string) error {
	return s.Validate(catProxiesTargetValue(country), catProxiesTargetValue(state), catProxiesTargetValue(city))
}

func catProxiesTargetValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *CatProxiesTargetingService) Validate(country, state, city string) error {
	if s == nil {
		return fmt.Errorf("CatProxies targeting service is unavailable")
	}
	country = normalizeCatProxiesTargetToken(country)
	state = normalizeCatProxiesTargetToken(state)
	city = normalizeCatProxiesTargetToken(city)
	if country == "" {
		if state != "" || city != "" {
			return fmt.Errorf("country is required when state or city is set")
		}
		return nil
	}
	if _, ok := s.countries[country]; !ok {
		return fmt.Errorf("unsupported CatProxies country %q", country)
	}
	if state != "" {
		if country != "us" {
			return fmt.Errorf("state targeting is supported only for country us")
		}
		if _, ok := s.states[state]; !ok {
			return fmt.Errorf("unsupported CatProxies US state %q", state)
		}
	}
	if city == "" {
		return nil
	}
	cities := s.snapshot.Cities[country]
	if country == "us" && state != "" {
		cities = s.snapshot.USStateCities[state]
	}
	for _, candidate := range cities {
		if normalizeCatProxiesTargetToken(candidate) == city {
			return nil
		}
	}
	return fmt.Errorf("unsupported CatProxies city %q for selected target", city)
}

func normalizeCatProxiesTargetToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
